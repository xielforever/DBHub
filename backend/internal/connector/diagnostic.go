package connector

// Diagnostic contains only stable, platform-authored text. Raw driver messages,
// URLs, credentials, and SQL are never serialized as connection-check results.
type Diagnostic struct {
	Code   string `json:"code"`
	Title  string `json:"title"`
	Hint   string `json:"hint"`
	Action string `json:"action"`
}
