// The Go side asks for a confirmation by rejecting with one of these texts
// (internal/app/setup_service.go); the caller asks again with confirm set.

const CONFIRM_ADOPT = "confirm-adopt";
const CONFIRM_FRESH = "confirm-fresh";

/** The folder already holds data and must be confirmed before it is used. */
export const isConfirmAdopt = (errText: string): boolean => errText.includes(CONFIRM_ADOPT);

/** The folder holds no data and an empty list must be confirmed before it is started. */
export const isConfirmFresh = (errText: string): boolean => errText.includes(CONFIRM_FRESH);
