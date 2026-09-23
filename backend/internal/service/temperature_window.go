package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/constants"
	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/dto"
	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/model"
	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/repository"
)

type TemperatureWindowService interface {
	List(context.Context, dto.PageQuery) (repository.Page[model.TemperatureWindow], error)
	Get(context.Context, uint) (model.TemperatureWindow, error)
	Create(context.Context, dto.CreateTemperatureWindow, string, string) (model.TemperatureWindow, error)
	Update(context.Context, uint, dto.UpdateTemperatureWindow, string, string) (model.TemperatureWindow, error)
	Transition(context.Context, uint, dto.TransitionRequest, string, string) (model.TemperatureWindow, error)
	Delete(context.Context, uint, string, string) error
	StatusCounts(context.Context) (map[string]int64, error)
}

// ActivationBlockedError carries the persisted draft (with block metadata) back
// to the HTTP layer so the 422 response can include the impact list while the
// draft itself remains unchanged.
type ActivationBlockedError struct {
	Window model.TemperatureWindow
	Reason string
}

func (e *ActivationBlockedError) Error() string { return e.Reason }
func (e *ActivationBlockedError) Unwrap() error { return ErrActivationBlocked }

type temperatureWindowService struct {
	repository repository.TemperatureWindowRepository
	containers repository.TransportContainerRepository
	excursions repository.ExcursionEventRepository
	security   SecurityService
}

func NewTemperatureWindowService(repo repository.TemperatureWindowRepository, containers repository.TransportContainerRepository, excursions repository.ExcursionEventRepository, security SecurityService) TemperatureWindowService {
	return &temperatureWindowService{repository: repo, containers: containers, excursions: excursions, security: security}
}

func (s *temperatureWindowService) List(ctx context.Context, query dto.PageQuery) (repository.Page[model.TemperatureWindow], error) {
	return s.repository.List(ctx, query)
}

func (s *temperatureWindowService) Get(ctx context.Context, id uint) (model.TemperatureWindow, error) {
	return s.repository.Get(ctx, id)
}

func (s *temperatureWindowService) Create(ctx context.Context, input dto.CreateTemperatureWindow, actor, requestID string) (model.TemperatureWindow, error) {
	if err := validateTemperatureWindowBusinessFields(input.Code, input.Name, input.Facility, input.Owner); err != nil {
		return model.TemperatureWindow{}, err
	}
	item := model.TemperatureWindow{
		BaseModel: model.BaseModel{
			Code: strings.ToUpper(strings.TrimSpace(input.Code)), Name: strings.TrimSpace(input.Name),
			Status: model.TemperatureWindowInitialStatus, Version: 1, Description: strings.TrimSpace(input.Description),
		},
		Facility: strings.TrimSpace(input.Facility), Owner: strings.TrimSpace(input.Owner),
		Category: strings.TrimSpace(input.Category), RiskLevel: input.RiskLevel,
		MetricValue: input.MetricValue, MetricUnit: strings.TrimSpace(input.MetricUnit),
		EffectiveAt: input.EffectiveAt.UTC(), Evidence: strings.TrimSpace(input.Evidence),
		RelatedCode:    strings.ToUpper(strings.TrimSpace(input.RelatedCode)),
		ProductClass:   strings.TrimSpace(firstNonEmpty(input.ProductClass, input.Category)),
		MinimumCelsius: input.MinimumCelsius, MaximumCelsius: input.MaximumCelsius,
		MaxExcursionMinutes: input.MaxExcursionMinutes,
		QualityOwner:        strings.TrimSpace(firstNonEmpty(input.QualityOwner, input.Owner)),
	}
	if item.MaximumCelsius == 0 {
		item.MaximumCelsius = input.MetricValue
	}
	if item.MaximumCelsius <= item.MinimumCelsius {
		return model.TemperatureWindow{}, fmt.Errorf("%w: maximum temperature must exceed minimum temperature", ErrInvalidInput)
	}
	if err := s.repository.Create(ctx, &item); err != nil {
		return model.TemperatureWindow{}, fmt.Errorf("create 温控规则: %w", err)
	}
	_ = s.security.Audit(ctx, actor, requestID, "create", "TemperatureWindow", item.ID, "", item.Status, "created 温控规则")
	return item, nil
}

func (s *temperatureWindowService) Update(ctx context.Context, id uint, input dto.UpdateTemperatureWindow, actor, requestID string) (model.TemperatureWindow, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.TemperatureWindow{}, err
	}
	if err := validateTemperatureWindowBusinessFields(current.Code, input.Name, input.Facility, input.Owner); err != nil {
		return model.TemperatureWindow{}, err
	}
	current.Name = strings.TrimSpace(input.Name)
	current.Description = strings.TrimSpace(input.Description)
	current.Facility = strings.TrimSpace(input.Facility)
	current.Owner = strings.TrimSpace(input.Owner)
	current.Category = strings.TrimSpace(input.Category)
	current.RiskLevel = input.RiskLevel
	current.MetricValue = input.MetricValue
	current.MetricUnit = strings.TrimSpace(input.MetricUnit)
	current.EffectiveAt = input.EffectiveAt.UTC()
	current.Evidence = strings.TrimSpace(input.Evidence)
	current.RelatedCode = strings.ToUpper(strings.TrimSpace(input.RelatedCode))
	current.ProductClass = strings.TrimSpace(firstNonEmpty(input.ProductClass, input.Category))
	current.MinimumCelsius = input.MinimumCelsius
	current.MaximumCelsius = input.MaximumCelsius
	if current.MaximumCelsius == 0 {
		current.MaximumCelsius = input.MetricValue
	}
	current.MaxExcursionMinutes = input.MaxExcursionMinutes
	current.QualityOwner = strings.TrimSpace(firstNonEmpty(input.QualityOwner, input.Owner))
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	if err := s.repository.Update(ctx, id, input.ExpectedVersion, &current); err != nil {
		return model.TemperatureWindow{}, fmt.Errorf("update 温控规则: %w", err)
	}
	_ = s.security.Audit(ctx, actor, requestID, "update", "TemperatureWindow", id, current.Status, current.Status, "updated business fields")
	return s.repository.Get(ctx, id)
}

func (s *temperatureWindowService) Transition(ctx context.Context, id uint, input dto.TransitionRequest, actor, requestID string) (model.TemperatureWindow, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.TemperatureWindow{}, err
	}
	target := strings.TrimSpace(input.Status)
	if !constants.CanTransition(constants.TemperatureWindowTransitions, current.Status, target) {
		return model.TemperatureWindow{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current.Status, target)
	}
	if target == "active" {
		return s.activate(ctx, current, input.ExpectedVersion, input.Reason, actor, requestID)
	}
	before := current.Status
	current.Status = target
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	if err := s.repository.Update(ctx, id, input.ExpectedVersion, &current); err != nil {
		return model.TemperatureWindow{}, fmt.Errorf("transition 温控规则: %w", err)
	}
	if err := s.security.Audit(ctx, actor, requestID, "transition", "TemperatureWindow", id, before, target, input.Reason); err != nil {
		return model.TemperatureWindow{}, fmt.Errorf("persist transition audit: %w", err)
	}
	return s.repository.Get(ctx, id)
}

// activate performs the impact check before a draft 温控规则 becomes effective.
// Any 仍在途容器 or 未结束偏差 for the same product class and 场站 blocks the whole
// activation: the draft keeps its status and version, the previous active rule
// is not touched, and the blocking codes plus reason are persisted on the draft
// so they remain readable after the failed response. Once the impacts are
// cleared, submitting again activates the draft and supersedes the old rule.
func (s *temperatureWindowService) activate(ctx context.Context, current model.TemperatureWindow, expectedVersion uint, reason, actor, requestID string) (model.TemperatureWindow, error) {
	productClass := strings.TrimSpace(current.ProductClass)
	facility := strings.TrimSpace(current.Facility)

	containers, err := s.containers.InTransitForScope(ctx, productClass, facility)
	if err != nil {
		return model.TemperatureWindow{}, fmt.Errorf("check in-transit containers: %w", err)
	}
	excursions, err := s.excursions.OpenForScope(ctx, productClass, facility)
	if err != nil {
		return model.TemperatureWindow{}, fmt.Errorf("check unfinished excursions: %w", err)
	}

	if len(containers) > 0 || len(excursions) > 0 {
		impacts := make([]model.ActivationImpact, 0, len(containers)+len(excursions))
		details := make([]string, 0, len(containers)+len(excursions))
		for _, container := range containers {
			impacts = append(impacts, model.ActivationImpact{Type: "container", Code: container.Code, Name: container.Name, Status: container.Status})
			details = append(details, fmt.Sprintf("container:%s", container.Code))
		}
		for _, excursion := range excursions {
			impacts = append(impacts, model.ActivationImpact{Type: "excursion", Code: excursion.Code, Name: excursion.Name, Status: excursion.Status})
			details = append(details, fmt.Sprintf("excursion:%s", excursion.Code))
		}
		blockReason := fmt.Sprintf("blocked by %d in-flight impact(s): %s", len(impacts), strings.Join(details, ", "))
		blockedAt := time.Now().UTC()
		// Persist the block metadata only; status, version and the existing
		// active rule all stay unchanged, so a retry with the same
		// expectedVersion remains valid after the impacts are resolved.
		if persistErr := s.repository.RecordActivationBlock(ctx, current.ID, blockReason, impacts, blockedAt); persistErr != nil {
			return model.TemperatureWindow{}, fmt.Errorf("persist activation block: %w", persistErr)
		}
		_ = s.security.Audit(ctx, actor, requestID, "activation_blocked", "TemperatureWindow", current.ID, current.Status, current.Status, blockReason)
		blocked, getErr := s.repository.Get(ctx, current.ID)
		if getErr != nil {
			return model.TemperatureWindow{}, getErr
		}
		return blocked, &ActivationBlockedError{Window: blocked, Reason: blockReason}
	}

	before := current.Status
	now := time.Now().UTC()
	current.Status = "active"
	current.Version = expectedVersion + 1
	current.UpdatedAt = now
	current.LastBlockedAt = nil
	current.LastBlockReason = ""
	current.LastBlockImpact = nil
	audit := auditLog(actor, requestID, "transition", "TemperatureWindow", current.ID, before, "active", reason)
	if err := s.repository.ActivateWithSupersession(ctx, current.ID, expectedVersion, current, productClass, facility, audit); err != nil {
		return model.TemperatureWindow{}, fmt.Errorf("transition 温控规则: %w", err)
	}
	return s.repository.Get(ctx, current.ID)
}

func (s *temperatureWindowService) Delete(ctx context.Context, id uint, actor, requestID string) error {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := s.repository.Delete(ctx, id); err != nil {
		return err
	}
	return s.security.Audit(ctx, actor, requestID, "delete", "TemperatureWindow", id, current.Status, "deleted", "soft deleted 温控规则")
}

func (s *temperatureWindowService) StatusCounts(ctx context.Context) (map[string]int64, error) {
	return s.repository.CountByStatus(ctx)
}

func validateTemperatureWindowBusinessFields(code, name, facility, owner string) error {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(name) == "" || strings.TrimSpace(facility) == "" || strings.TrimSpace(owner) == "" {
		return ErrInvalidInput
	}
	return nil
}
