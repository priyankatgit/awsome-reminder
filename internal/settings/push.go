package settings

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

// DefaultPushServer is the public ntfy server.
const DefaultPushServer = "https://ntfy.sh"

// PushGraceOptions are the allowed values of Settings.PushGrace.
var PushGraceOptions = []time.Duration{0, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute}

var pushTopicRE = regexp.MustCompile(`^[-_A-Za-z0-9]{1,64}$`)

// NewPushTopic returns a fresh secret topic: "ar-" and 26 lowercase base32
// characters (130 bits) from crypto/rand.
func NewPushTopic() string {
	b := make([]byte, 17) // 136 bits; 26 chars use the first 130
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	return "ar-" + strings.ToLower(enc[:26])
}

// validatePush checks the push fields. An enabled push needs a topic (the core
// fills one in before validating).
func (s Settings) validatePush() error {
	u, err := url.Parse(s.PushServer)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("pushServer: want an http(s) URL, got %q", s.PushServer)
	}
	if s.PushTopic != "" && !pushTopicRE.MatchString(s.PushTopic) {
		return fmt.Errorf("pushTopic: use 1-64 letters, digits, - or _")
	}
	if s.PushEnabled && s.PushTopic == "" {
		return fmt.Errorf("pushTopic: required when phone push is on")
	}
	if !slices.Contains(PushGraceOptions, s.PushGrace) {
		return fmt.Errorf("pushGrace: %v is not one of 0, 2m, 5m, 10m", s.PushGrace)
	}
	return nil
}
