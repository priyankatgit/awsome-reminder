package app

import (
	"errors"

	"awesome-reminder/internal/datamove"
	"awesome-reminder/internal/paths"
)

// DataFolderInfo is what the Settings "Data folder" section shows.
type DataFolderInfo struct {
	Path         string `json:"path"`
	UsedText     string `json:"usedText"`     // e.g. "84 MB"
	FreeText     string `json:"freeText"`     // e.g. "41 GB free"; "" when unknown
	PreviousDir  string `json:"previousDir"`  // "" when there is no old copy
	PreviousText string `json:"previousText"` // size of the old copy
}

const errSameFolder = "This is already your data folder."

// currentLayout is the data folder in use.
func (s *Service) currentLayout() (paths.Layout, error) {
	if s.d.DataDir == "" {
		return paths.Layout{}, errors.New("The data folder is not known.")
	}
	return paths.NewLayout(s.d.DataDir)
}

// DataFolder describes the data folder in use and any old copy left behind.
func (s *Service) DataFolder() (DataFolderInfo, error) {
	lay, err := s.currentLayout()
	if err != nil {
		return DataFolderInfo{}, err
	}
	info := DataFolderInfo{Path: lay.Root}
	if sz, err := datamove.Measure(lay); err == nil {
		info.UsedText = datamove.FormatBytes(sz.Total())
	}
	if free, err := paths.FreeSpace(lay.Root); err == nil {
		info.FreeText = datamove.FormatBytes(int64(free)) + " free"
	}
	loc, found, err := paths.LoadLocation()
	if err != nil {
		return DataFolderInfo{}, err
	}
	if found && loc.PreviousDir != "" {
		info.PreviousDir = loc.PreviousDir
		info.PreviousText = datamove.FormatBytes(datamove.OldCopySize(loc.PreviousDir))
	}
	return info, nil
}

// CheckDataFolder inspects a folder picked for the new data folder.
func (s *Service) CheckDataFolder(picked string) datamove.Check {
	lay, err := s.currentLayout()
	if err != nil {
		return datamove.Check{Path: picked, Err: err.Error()}
	}
	return datamove.CheckTarget(picked, &lay)
}

// ChangeDataFolder switches to the picked folder and restarts. A folder that
// already holds data needs confirm (error "confirm-adopt") and is used as it
// is; any other folder gets a copy of the current data at the next start.
func (s *Service) ChangeDataFolder(picked string, confirm bool) error {
	c := s.CheckDataFolder(picked)
	if c.Err != "" {
		return errors.New(c.Err)
	}
	if c.Same {
		return errors.New(errSameFolder)
	}
	if c.HasData {
		if !confirm {
			return errors.New(errConfirmAdopt)
		}
		loc, _, err := paths.LoadLocation()
		if err != nil {
			return err
		}
		if loc.DataDir == "" {
			loc.DataDir = s.d.DataDir
		}
		if err := datamove.Adopt(c.Path, &loc); err != nil {
			return err
		}
	} else if err := datamove.RequestMove(c.Path); err != nil {
		return err
	}
	if s.d.Restart != nil {
		s.d.Restart()
	}
	return nil
}

// RemoveOldCopy deletes what the app left in the previous data folder.
func (s *Service) RemoveOldCopy() error {
	loc, found, err := paths.LoadLocation()
	if err != nil {
		return err
	}
	if !found {
		return errors.New("There is no old copy to remove.")
	}
	return datamove.RemoveOldCopy(loc)
}

// OpenDataFolder shows the data folder in the file manager.
func (s *Service) OpenDataFolder() error {
	if s.d.DataDir == "" {
		return errors.New("The data folder is not known.")
	}
	return openFolder(s.d.DataDir)
}
