package model

import "time"

// ComplianceDecision models 合规决定 as an independently versioned aggregate. The fields
// cover ownership, operational context, evidence and measured risk so later
// changes naturally span persistence, service and UI layers.
type ComplianceDecision struct {
	BaseModel
	Facility    string             `json:"facility" gorm:"size:120;index"`
	Owner       string             `json:"owner" gorm:"size:120;index"`
	Category    string             `json:"category" gorm:"size:80;index"`
	RiskLevel   string             `json:"riskLevel" gorm:"size:32;index"`
	MetricValue float64            `json:"metricValue"`
	MetricUnit  string             `json:"metricUnit" gorm:"size:24"`
	EffectiveAt time.Time          `json:"effectiveAt"`
	Evidence    string             `json:"evidence" gorm:"size:2000"`
	RelatedCode string             `json:"relatedCode" gorm:"size:64;index"`
	Revisions   []DecisionRevision `json:"revisions" gorm:"foreignKey:ComplianceDecisionID;constraint:OnDelete:CASCADE"`

	// ReviewGate 是每次进入复核/最终判定时按关联装置重读许可规则与已核验样本后
	// 计算出的实时核验视图。它不入库、不参与版本，只反映当前生效规则与最新证据。
	ReviewGate *ReviewGate `json:"reviewGate,omitempty" gorm:"-"`
}

// ReviewGateOutcome 描述复核闭环允许的操作结论。
type ReviewGateOutcome string

const (
	// ReviewGateReady 表示样本、装置、阈值全部一致，复核人可以接受。
	ReviewGateReady ReviewGateOutcome = "ready"
	// ReviewGateBlocked 表示样本缺失或装置不一致，原状态保留，必须先修复证据。
	ReviewGateBlocked ReviewGateOutcome = "blocked"
	// ReviewGateExceeded 表示读数超过当前生效许可阈值，只能升级处理。
	ReviewGateExceeded ReviewGateOutcome = "exceeded"
)

// ReviewGate 是复核闭环针对单个决定重新读取当前生效许可规则与最新已核验样本后
// 得到的只读核验快照。失败时 Reasons 列出必须保留原状态的具体原因。
type ReviewGate struct {
	Outcome         ReviewGateOutcome `json:"outcome"`
	UnitCode        string            `json:"unitCode,omitempty"`
	RuleCode        string            `json:"ruleCode,omitempty"`
	RuleVersion     uint              `json:"ruleVersion,omitempty"`
	RuleThreshold   float64           `json:"ruleThreshold,omitempty"`
	MetricUnit      string            `json:"metricUnit,omitempty"`
	SampleCode      string            `json:"sampleCode,omitempty"`
	SampleReading   float64           `json:"sampleReading,omitempty"`
	SampleStatus    string            `json:"sampleStatus,omitempty"`
	WithinThreshold bool              `json:"withinThreshold"`
	AllowedAction   string            `json:"allowedAction,omitempty"`
	Reasons         []string          `json:"reasons"`
	CheckedAt       time.Time         `json:"checkedAt"`
}

func (item *ComplianceDecision) GetBase() *BaseModel { return &item.BaseModel }

func (item ComplianceDecision) TableName() string { return "compliance_decisions" }

var ComplianceDecisionInitialStatus = "draft"

// DecisionRevision is an append-only compliance snapshot. It keeps the
// evidence and request context that justified every aggregate version.
type DecisionRevision struct {
	ID                   uint      `json:"id" gorm:"primaryKey"`
	ComplianceDecisionID uint      `json:"complianceDecisionId" gorm:"uniqueIndex:idx_decision_revision_version;not null"`
	Version              uint      `json:"version" gorm:"uniqueIndex:idx_decision_revision_version;not null"`
	State                string    `json:"state" gorm:"size:40;not null"`
	Evidence             string    `json:"evidence" gorm:"size:2000;not null"`
	Reason               string    `json:"reason" gorm:"size:500;not null"`
	Actor                string    `json:"actor" gorm:"size:80;not null;index"`
	RequestID            string    `json:"requestId" gorm:"size:64;not null;index"`
	CreatedAt            time.Time `json:"createdAt" gorm:"index"`
}
