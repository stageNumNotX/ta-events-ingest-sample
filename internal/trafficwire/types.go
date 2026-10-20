// Package trafficwire разбирает pinned JSON traffic-runtime/v1.
// Хеш считается по сырым байтам до parse; повторной сериализации нет.
package trafficwire

import "encoding/json"

const (
	CompileTarget           = "traffic-runtime/v1"
	ArtifactRelease         = "traffic-release/v1"
	ArtifactProgram         = "traffic-program/v1"
	ArtifactWeights         = "traffic-weights/v1"
	HashAlgorithmV1         = "traffic.hash.v1"
	HandlerRedirectTarget   = "redirect-target-v1"
	HandlerRedirectTargetV2 = "redirect-target-v2"
	TypeRedirectTarget      = "redirect-target"
	ChoiceDomainScenario    = "SCENARIO"
	ChoiceDomainVariant     = "VARIANT"
	OpCondition             = "CONDITION"
	OpALL                   = "ALL"
	OpANY                   = "ANY"
	OpNOT                   = "NOT"
	OperatorEQ              = "EQ"
	OperatorIN              = "IN"
	OperatorEXISTS          = "EXISTS"
	AssignmentSubjectID     = "SUBJECT_ID"
	AssignmentRequestID     = "REQUEST_ID"
	MaxMatcherDepth         = 16
	MaxINValues             = 200
)

type ArtifactRef struct {
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}

type SourceWindow struct {
	Start     string `json:"start"`
	End       string `json:"end"`
	Watermark string `json:"watermark"`
}

type ReleaseSource struct {
	ConfigurationRevision  string       `json:"configurationRevision"`
	TrafficPolicyRevision  string       `json:"trafficPolicyRevision"`
	TrafficSignalsRevision string       `json:"trafficSignalsRevision"`
	TrafficBoundsRevision  string       `json:"trafficBoundsRevision"`
	TrafficFixRevision     string       `json:"trafficFixRevision"`
	ContextRegistryVersion int          `json:"contextRegistryVersion"`
	ContentRegistryVersion int          `json:"contentRegistryVersion"`
	ModelVersion           string       `json:"modelVersion"`
	SourceWindow           SourceWindow `json:"sourceWindow"`
}

// Release ссылается только на program и weights. Поле redirectTargets
// (отдельный target registry) отклонено: unknown field → ошибка parse.
type Release struct {
	SchemaVersion int           `json:"schemaVersion"`
	ArtifactType  string        `json:"artifactType"`
	CompileTarget string        `json:"compileTarget"`
	LayoutID      string        `json:"layoutId"`
	Program       ArtifactRef   `json:"program"`
	Weights       ArtifactRef   `json:"weights"`
	Source        ReleaseSource `json:"source"`
}

type Scenario struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	SlotIDs []string `json:"slotIds"`
}

type Variant struct {
	ID             string          `json:"id"`
	TypeID         string          `json:"typeId"`
	SchemaVersion  int             `json:"schemaVersion"`
	HandlerVersion string          `json:"handlerVersion"`
	Payload        json.RawMessage `json:"payload"`
}

type Vector struct {
	DistributionKey string   `json:"distributionKey"`
	ChoiceDomain    string   `json:"choiceDomain"`
	ChoiceIDs       []string `json:"choiceIds"`
}

type Branch struct {
	VectorIndex int `json:"vectorIndex"`
}

// Matcher is the compiled `when` tree: ALL/ANY/NOT or CONDITION.
// CONDITION EQ uses value; IN uses values; EXISTS has neither.
type Matcher struct {
	Op       string          `json:"op"`
	Rules    []Matcher       `json:"rules,omitempty"`
	Rule     *Matcher        `json:"rule,omitempty"`
	Field    string          `json:"field,omitempty"`
	Operator string          `json:"operator,omitempty"`
	Value    json.RawMessage `json:"value,omitempty"`
	Values   json.RawMessage `json:"values,omitempty"`
}

type Rule struct {
	RuleID string  `json:"ruleId"`
	When   Matcher `json:"when"`
	Branch Branch  `json:"branch"`
}

type DecisionProgram struct {
	DecisionScopeID           string `json:"decisionScopeId"`
	AssignmentKey             string `json:"assignmentKey"`
	Salt                      string `json:"salt"`
	MissingAssignmentChoiceID string `json:"missingAssignmentChoiceId"`
	Rules                     []Rule `json:"rules"`
	DefaultBranch             Branch `json:"defaultBranch"`
}

type SlotProgram struct {
	ScenarioID    string          `json:"scenarioId"`
	SlotID        string          `json:"slotId"`
	TypeID        string          `json:"typeId"`
	SchemaVersion int             `json:"schemaVersion"`
	Program       DecisionProgram `json:"program"`
}

type Program struct {
	SchemaVersion          int             `json:"schemaVersion"`
	ArtifactType           string          `json:"artifactType"`
	CompileTarget          string          `json:"compileTarget"`
	LayoutID               string          `json:"layoutId"`
	TotalShare             int             `json:"totalShare"`
	HashAlgorithm          string          `json:"hashAlgorithm"`
	PolicyID               string          `json:"policyId"`
	ContextRegistryVersion int             `json:"contextRegistryVersion"`
	ContentRegistryVersion int             `json:"contentRegistryVersion"`
	Scenarios              []Scenario      `json:"scenarios"`
	Variants               []Variant       `json:"variants"`
	Vectors                []Vector        `json:"vectors"`
	ScenarioProgram        DecisionProgram `json:"scenarioProgram"`
	SlotPrograms           []SlotProgram   `json:"slotPrograms"`
}

type WeightVector struct {
	DistributionKey string `json:"distributionKey"`
	CumulativeEnds  []int  `json:"cumulativeEnds"`
}

type Weights struct {
	SchemaVersion int            `json:"schemaVersion"`
	ArtifactType  string         `json:"artifactType"`
	CompileTarget string         `json:"compileTarget"`
	LayoutID      string         `json:"layoutId"`
	Vectors       []WeightVector `json:"vectors"`
}

// RedirectTargetV1 — accepted compiler emit: targetId + enabled. Нет url.
type RedirectTargetV1 struct {
	Enabled  bool   `json:"enabled"`
	TargetID string `json:"targetId"`
}

// RedirectTargetV2 — schemaVersion=2 / handlerVersion=redirect-target-v2.
// url — исполняемое HTTPS-значение. Registry targetId→URL не используется.
type RedirectTargetV2 struct {
	Enabled  bool   `json:"enabled"`
	TargetID string `json:"targetId"`
	URL      string `json:"url"`
}

// RedirectTarget — нормализованный результат parse v1 или v2.
type RedirectTarget struct {
	Handler  string
	Schema   int
	Enabled  bool
	TargetID string
	URL      string
}
