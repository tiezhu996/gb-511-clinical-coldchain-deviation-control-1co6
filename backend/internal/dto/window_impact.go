package dto

import "time"

// WindowImpactSubject identifies the product class + facility scope whose in-transit
// containers and open excursions must be cleared before a draft becomes active.
type WindowImpactSubject struct {
	ProductClass string `json:"productClass"`
	Facility     string `json:"facility"`
}

// ActivationImpact is the pre-activation verification result returned to the temperature
// control workbench. Empty code lists mean the draft is safe to activate.
type ActivationImpact struct {
	WindowID         uint      `json:"windowId"`
	WindowCode       string    `json:"windowCode"`
	ProductClass     string    `json:"productClass"`
	Facility         string    `json:"facility"`
	ContainerCodes   []string  `json:"containerCodes"`
	ExcursionCodes   []string  `json:"excursionCodes"`
	Blocked           bool       `json:"blocked"`
	LastBlockedReason string     `json:"lastBlockedReason,omitempty"`
	LastBlockedAt     *time.Time `json:"lastBlockedAt,omitempty"`
}
