package model

import (
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

// TemperatureWindow models 温控规则 as an independently versioned aggregate. The fields
// cover ownership, operational context, evidence and measured risk so later
// changes naturally span persistence, service and UI layers.
type TemperatureWindow struct {
	BaseModel
	ProductClass        string    `json:"productClass" gorm:"size:100;index"`
	MinimumCelsius      float64   `json:"minimumCelsius"`
	MaximumCelsius      float64   `json:"maximumCelsius"`
	MaxExcursionMinutes int       `json:"maxExcursionMinutes"`
	QualityOwner        string    `json:"qualityOwner" gorm:"size:120"`
	Facility            string    `json:"facility" gorm:"size:120;index"`
	Owner               string    `json:"owner" gorm:"size:120;index"`
	Category            string    `json:"category" gorm:"size:80;index"`
	RiskLevel           string    `json:"riskLevel" gorm:"size:32;index"`
	MetricValue         float64   `json:"metricValue"`
	MetricUnit          string    `json:"metricUnit" gorm:"size:24"`
	EffectiveAt         time.Time `json:"effectiveAt"`
	Evidence            string    `json:"evidence" gorm:"size:2000"`
	RelatedCode         string    `json:"relatedCode" gorm:"size:64;index"`
	// LastBlockedContainerCodes / LastBlockedExcursionCodes persist the impact list from the
	// most recent failed activation so a later read-back still shows why 生效 was refused.
	LastBlockedContainerCodes string    `json:"-" gorm:"column:last_blocked_container_codes;size:2000"`
	LastBlockedExcursionCodes string    `json:"-" gorm:"column:last_blocked_excursion_codes;size:2000"`
	LastBlockedReason         string    `json:"lastBlockedReason" gorm:"size:500"`
	LastBlockedAt             time.Time `json:"lastBlockedAt"`
	// BlockedContainerCodes / BlockedExcursionCodes are hydrated from the persisted JSON
	// above; they are never written to the database directly.
	BlockedContainerCodes []string `json:"blockedContainerCodes" gorm:"-"`
	BlockedExcursionCodes []string `json:"blockedExcursionCodes" gorm:"-"`
}

// AfterFind restores the human-readable impact lists persisted as JSON arrays.
func (item *TemperatureWindow) AfterFind(_ *gorm.DB) error {
	item.BlockedContainerCodes = decodeCodeList(item.LastBlockedContainerCodes)
	item.BlockedExcursionCodes = decodeCodeList(item.LastBlockedExcursionCodes)
	return nil
}

func decodeCodeList(raw string) []string {
	if raw == "" {
		return []string{}
	}
	codes := []string{}
	if err := json.Unmarshal([]byte(raw), &codes); err != nil {
		return []string{}
	}
	return codes
}

// EncodeCodeList serialises an impact list for persistence; an empty list is stored as "".
func EncodeCodeList(codes []string) string {
	if len(codes) == 0 {
		return ""
	}
	encoded, err := json.Marshal(codes)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func (item *TemperatureWindow) GetBase() *BaseModel { return &item.BaseModel }

func (item TemperatureWindow) TableName() string { return "temperature_windows" }

var TemperatureWindowInitialStatus = "draft"
