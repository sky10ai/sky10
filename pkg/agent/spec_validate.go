package agent

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	agentRuntimePackagePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+.-]*$`)
	agentRuntimeImagePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]*$`)
)

func validateAgentSpec(spec AgentSpec) error {
	if spec.Spec != AgentSpecVersion {
		return fmt.Errorf("spec must be %s", AgentSpecVersion)
	}
	if strings.TrimSpace(spec.ID) == "" {
		return fmt.Errorf("spec.id is required")
	}
	if strings.TrimSpace(spec.Prompt) == "" {
		return fmt.Errorf("spec.prompt is required")
	}
	if strings.TrimSpace(spec.Name) == "" {
		return fmt.Errorf("spec.name is required")
	}
	switch spec.Status {
	case SpecStatusDraft, SpecStatusApproved, SpecStatusDiscarded:
	default:
		return fmt.Errorf("spec.status must be draft, approved, or discarded")
	}
	if err := validateTimestamp("spec.created_at", spec.CreatedAt); err != nil {
		return err
	}
	if err := validateTimestamp("spec.updated_at", spec.UpdatedAt); err != nil {
		return err
	}
	if spec.ApprovedAt != "" {
		if err := validateTimestamp("spec.approved_at", spec.ApprovedAt); err != nil {
			return err
		}
	}
	if err := validateRuntimeSpec(spec.Runtime); err != nil {
		return err
	}
	if err := validateFulfillment(spec.Fulfillment); err != nil {
		return fmt.Errorf("spec.fulfillment: %w", err)
	}
	if len(spec.Tools) == 0 {
		return fmt.Errorf("spec.tools must contain at least one tool")
	}
	for i, tool := range spec.Tools {
		if err := validateToolSpec(tool); err != nil {
			return fmt.Errorf("spec.tools[%d]: %w", i, err)
		}
	}
	if err := validatePublishPolicy(spec.PublishPolicy); err != nil {
		return err
	}
	return nil
}

func validateRuntimeSpec(runtime AgentRuntimeSpec) error {
	switch runtime.Target {
	case "sandbox", "local", "remote":
	default:
		return fmt.Errorf("spec.runtime.target must be sandbox, local, or remote")
	}
	for i, pkg := range runtime.Packages {
		if err := validateRuntimePackage(pkg); err != nil {
			return fmt.Errorf("spec.runtime.packages[%d]: %w", i, err)
		}
	}
	for i, container := range runtime.Containers {
		if err := validateRuntimeImage(container.Image); err != nil {
			return fmt.Errorf("spec.runtime.containers[%d].image: %w", i, err)
		}
		for j, pkg := range container.Packages {
			if err := validateRuntimePackage(pkg); err != nil {
				return fmt.Errorf("spec.runtime.containers[%d].packages[%d]: %w", i, j, err)
			}
		}
	}
	return nil
}

func validateRuntimeImage(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if !agentRuntimeImagePattern.MatchString(value) {
		return fmt.Errorf("image %q must be a Docker image reference", value)
	}
	return nil
}

func validateRuntimePackage(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("package name is required")
	}
	if !agentRuntimePackagePattern.MatchString(value) {
		return fmt.Errorf("package name %q must be an apt-style package name", value)
	}
	return nil
}

func validateToolSpec(tool AgentToolSpec) error {
	if strings.TrimSpace(tool.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if strings.TrimSpace(tool.Description) == "" {
		return fmt.Errorf("description is required")
	}
	if err := validateAudience(tool.Audience); err != nil {
		return err
	}
	if err := validateScope(tool.Scope); err != nil {
		return err
	}
	if err := validateAvailability(tool.Availability); err != nil {
		return err
	}
	if err := validateFulfillment(tool.Fulfillment); err != nil {
		return err
	}
	if err := validatePricing(tool.Pricing); err != nil {
		return err
	}
	return nil
}

func validatePublishPolicy(policy AgentPublishPolicy) error {
	if err := validateAudience(policy.Audience); err != nil {
		return fmt.Errorf("spec.publish_policy: %w", err)
	}
	if err := validateScope(policy.Scope); err != nil {
		return fmt.Errorf("spec.publish_policy: %w", err)
	}
	return nil
}

func validateAudience(value string) error {
	switch value {
	case "private", "public":
		return nil
	default:
		return fmt.Errorf("audience must be private or public")
	}
}

func validateScope(value string) error {
	switch value {
	case "current", "trusted", "explicit":
		return nil
	default:
		return fmt.Errorf("scope must be current, trusted, or explicit")
	}
}

func validateAvailability(value AgentAvailability) error {
	switch value.Status {
	case "available", "busy", "degraded", "unavailable":
		return nil
	default:
		return fmt.Errorf("availability.status must be available, busy, degraded, or unavailable")
	}
}

func validateFulfillment(value AgentFulfillment) error {
	switch value.Mode {
	case "autonomous", "assisted", "manual", "unspecified":
		return nil
	default:
		return fmt.Errorf("fulfillment.mode must be autonomous, assisted, manual, or unspecified")
	}
}

func validatePricing(value AgentPricingSpec) error {
	switch value.Model {
	case "free", "fixed", "variable", "per_interval":
	default:
		return fmt.Errorf("pricing.model must be free, fixed, variable, or per_interval")
	}
	if value.Model != "free" && value.PaymentAsset == nil {
		return fmt.Errorf("pricing.payment_asset is required for paid tools")
	}
	return nil
}

func validateTimestamp(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
		return fmt.Errorf("%s must be RFC3339: %w", field, err)
	}
	return nil
}
