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

// ErrActivationBlocked indicates a draft -> active transition was refused because the same
// product class and facility still has in-transit containers or unfinished excursions.
var ErrActivationBlocked = fmt.Errorf("temperature window activation blocked by in-flight containers or open excursions")

// ActivationBlockedError carries the impact lists that caused the activation refusal so the
// HTTP layer can echo them back to the temperature control workbench.
type ActivationBlockedError struct {
	Impact dto.ActivationImpact
}

func (e *ActivationBlockedError) Error() string {
	return fmt.Sprintf("%v: containers %s; excursions %s", ErrActivationBlocked,
		strings.Join(e.Impact.ContainerCodes, ","), strings.Join(e.Impact.ExcursionCodes, ","))
}

type TemperatureWindowService interface {
	List(context.Context, dto.PageQuery) (repository.Page[model.TemperatureWindow], error)
	Get(context.Context, uint) (model.TemperatureWindow, error)
	Create(context.Context, dto.CreateTemperatureWindow, string, string) (model.TemperatureWindow, error)
	Update(context.Context, uint, dto.UpdateTemperatureWindow, string, string) (model.TemperatureWindow, error)
	Transition(context.Context, uint, dto.TransitionRequest, string, string) (model.TemperatureWindow, error)
	Delete(context.Context, uint, string, string) error
	StatusCounts(context.Context) (map[string]int64, error)
	ActivationImpact(context.Context, uint) (dto.ActivationImpact, error)
}

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
		BlockedContainerCodes: []string{},
		BlockedExcursionCodes: []string{},
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
	before := current.Status

	// A draft can only take effect once every in-transit container and unfinished
	// excursion for the same product class and facility has been dealt with.
	if target == "active" && before == "draft" {
		impact, err := s.buildImpact(ctx, current)
		if err != nil {
			return model.TemperatureWindow{}, err
		}
		if impact.Blocked {
			reason := buildBlockedReason(impact.ContainerCodes, impact.ExcursionCodes)
			now := time.Now().UTC()
			if persistErr := s.repository.PersistActivationBlock(ctx, id,
				model.EncodeCodeList(impact.ContainerCodes), model.EncodeCodeList(impact.ExcursionCodes),
				reason, now,
				auditLog(actor, requestID, "activation_blocked", "TemperatureWindow", id, before, before,
					reason)); persistErr != nil {
				return model.TemperatureWindow{}, fmt.Errorf("persist activation block: %w", persistErr)
			}
			impact.LastBlockedReason = reason
			impact.LastBlockedAt = &now
			return model.TemperatureWindow{}, &ActivationBlockedError{Impact: impact}
		}

		now := time.Now().UTC()
		current.Status = target
		current.Version = input.ExpectedVersion + 1
		current.UpdatedAt = now
		// The last block marker is intentionally retained after activation: read-back of the
		// now-active rule must still show why the previous submission was refused. The full
		// activation_blocked audit trail is preserved as well.

		transitionAudit := auditLog(actor, requestID, "transition", "TemperatureWindow", id, before, target, input.Reason)
		audits := []*model.AuditLog{transitionAudit}
		peerAudits, err := s.supersededPeerAudits(ctx, id, current.ProductClass, current.Facility, actor, requestID, now)
		if err != nil {
			return model.TemperatureWindow{}, err
		}
		audits = append(audits, peerAudits...)
		if err := s.repository.ActivateDraft(ctx, id, input.ExpectedVersion, &current, current.ProductClass, current.Facility, audits...); err != nil {
			return model.TemperatureWindow{}, fmt.Errorf("transition 温控规则: %w", err)
		}
		return s.repository.Get(ctx, id)
	}

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

// ActivationImpact performs the pre-activation verification without mutating anything. The
// temperature control page uses it to render the impact list and block explanation.
func (s *temperatureWindowService) ActivationImpact(ctx context.Context, id uint) (dto.ActivationImpact, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return dto.ActivationImpact{}, err
	}
	return s.buildImpact(ctx, current)
}

func (s *temperatureWindowService) buildImpact(ctx context.Context, current model.TemperatureWindow) (dto.ActivationImpact, error) {
	productClass := strings.TrimSpace(firstNonEmpty(current.ProductClass, current.Category))
	facility := strings.TrimSpace(current.Facility)
	blockedContainers, err := s.containers.ListInTransitInScope(ctx, productClass, facility)
	if err != nil {
		return dto.ActivationImpact{}, fmt.Errorf("query in-transit containers: %w", err)
	}
	blockedExcursions, err := s.excursions.ListUnfinishedInScope(ctx, productClass, facility)
	if err != nil {
		return dto.ActivationImpact{}, fmt.Errorf("query unfinished excursions: %w", err)
	}
	impact := dto.ActivationImpact{
		WindowID:       current.ID,
		WindowCode:     current.Code,
		ProductClass:   productClass,
		Facility:       facility,
		ContainerCodes: codesFromContainers(blockedContainers),
		ExcursionCodes: codesFromExcursions(blockedExcursions),
		Blocked:        len(blockedContainers) > 0 || len(blockedExcursions) > 0,
		LastBlockedReason: strings.TrimSpace(current.LastBlockedReason),
	}
	if !current.LastBlockedAt.IsZero() {
		blockedAt := current.LastBlockedAt
		impact.LastBlockedAt = &blockedAt
	}
	return impact, nil
}

// supersededPeerAudits builds one transition audit per active rule that is about to be
// replaced by the newly activated rule.
func (s *temperatureWindowService) supersededPeerAudits(ctx context.Context, draftID uint, productClass, facility, actor, requestID string, now time.Time) ([]*model.AuditLog, error) {
	peers, err := s.repository.ListActiveInScope(ctx, draftID, productClass, facility)
	if err != nil {
		return nil, fmt.Errorf("query active 温控规则: %w", err)
	}
	detail := fmt.Sprintf("superseded by %d", draftID)
	audits := make([]*model.AuditLog, 0, len(peers))
	for _, peer := range peers {
		entry := auditLog(actor, requestID, "transition", "TemperatureWindow", peer.ID, "active", "superseded", detail)
		entry.CreatedAt = now
		audits = append(audits, entry)
	}
	return audits, nil
}

func codesFromContainers(items []model.TransportContainer) []string {
	codes := make([]string, 0, len(items))
	for _, item := range items {
		codes = append(codes, item.Code)
	}
	return codes
}

func codesFromExcursions(items []model.ExcursionEvent) []string {
	codes := make([]string, 0, len(items))
	for _, item := range items {
		codes = append(codes, item.Code)
	}
	return codes
}

func buildBlockedReason(containerCodes, excursionCodes []string) string {
	parts := make([]string, 0, 2)
	if len(containerCodes) > 0 {
		parts = append(parts, fmt.Sprintf("在途容器 %s 尚未完成运输", strings.Join(containerCodes, "、")))
	}
	if len(excursionCodes) > 0 {
		parts = append(parts, fmt.Sprintf("未结束偏差 %s 尚未关闭", strings.Join(excursionCodes, "、")))
	}
	return "温控规则生效被阻断：" + strings.Join(parts, "；") + "，处理完毕后再次提交才可生效"
}

func validateTemperatureWindowBusinessFields(code, name, facility, owner string) error {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(name) == "" || strings.TrimSpace(facility) == "" || strings.TrimSpace(owner) == "" {
		return ErrInvalidInput
	}
	return nil
}
