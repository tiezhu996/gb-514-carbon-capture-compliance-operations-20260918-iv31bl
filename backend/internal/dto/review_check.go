package dto

// ReviewReference is the minimal evidence snapshot of a linked aggregate. Only
// the fields needed to justify a compliance gate are exposed, so the review
// loop never mutates the device, rule or sample it re-reads.
type ReviewReference struct {
	ID          uint    `json:"id"`
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	Status      string  `json:"status"`
	Facility    string  `json:"facility"`
	MetricValue float64 `json:"metricValue"`
	MetricUnit  string  `json:"metricUnit"`
	Version     uint    `json:"version"`
}

// ReviewCheck is the re-read, re-verified context of a 合规决定 at the moment it
// enters review or reaches a final judgement. It is computed read-only and is
// attached both to the GET preview endpoint and to failed gate responses so
// the page can show why the original state was preserved.
type ReviewCheck struct {
	DecisionID     uint             `json:"decisionId"`
	DecisionCode   string           `json:"decisionCode"`
	RelatedCode    string           `json:"relatedCode"`
	CurrentState   string           `json:"currentState"`
	Unit           *ReviewReference `json:"unit,omitempty"`
	PermitRule     *ReviewReference `json:"permitRule,omitempty"`
	Sample         *ReviewReference `json:"sample,omitempty"`
	ThresholdValue float64          `json:"thresholdValue"`
	ThresholdUnit  string           `json:"thresholdUnit"`
	SampleValue    float64          `json:"sampleValue"`
	SampleUnit     string           `json:"sampleUnit"`
	// OverThreshold is meaningful only after every required reference exists.
	OverThreshold bool `json:"overThreshold"`
	// Blocked preserves the original state: the gate refuses to move.
	Blocked bool `json:"blocked"`
	// Reasons explains every sample-missing, device-mismatch or threshold
	// condition that drove the verdict; the page renders these verbatim.
	Reasons []string `json:"reasons"`
	// AllowedTargets lists the moves this snapshot permits for the requested
	// gate. Within threshold only acceptance is allowed; over threshold only
	// escalation is allowed.
	AllowedTargets []string `json:"allowedTargets"`
	// CheckedAt is the read time in UTC.
	CheckedAt string `json:"checkedAt"`
}
