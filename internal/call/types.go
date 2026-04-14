package call

// Objective is the structured task spec built during intake.
type Objective struct {
	Goal               string   `json:"goal"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	Constraints        []string `json:"constraints"`
	Notes              []string `json:"notes"`
	ModelHint          string   `json:"model_hint"` // "sonnet" or "opus"
	SpokenSummary      string   `json:"spoken_summary"`
}

// IntakeInput is what the session hands the PM on each intake call.
type IntakeInput struct {
	Transcript []string // alternating user/PM utterances, oldest first
	Latest     string   // the most recent user utterance
}

// RouteInput is what the session hands the PM on each ask_user routing call.
type RouteInput struct {
	Objective  Objective
	Transcript []string
	Question   string
}
