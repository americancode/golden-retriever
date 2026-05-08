package npm

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Overrides struct {
	rootSpecs map[string]string
	rules     []OverrideRule
}

type OverrideRule struct {
	Ancestors []OverrideSelector
	Target    OverrideSelector
	Spec      string
}

type OverrideConflictError struct {
	Name    string
	RawSpec string
	Spec    string
}

func (e *OverrideConflictError) Error() string {
	return fmt.Sprintf("override for %s@%s conflicts with direct dependency", e.Name, e.RawSpec)
}

type OverrideSelector struct {
	Name string
	Spec string
}

func ParseOverrides(raw json.RawMessage, rootSpecs map[string]string) (*Overrides, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("parse overrides: %w", err)
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("overrides must be an object")
	}
	overrides := &Overrides{rootSpecs: rootSpecs}
	if err := overrides.parseObject(nil, obj); err != nil {
		return nil, err
	}
	if err := overrides.validateRootConflicts(); err != nil {
		return nil, err
	}
	if len(overrides.rules) == 0 {
		return nil, nil
	}
	return overrides, nil
}

func (o *Overrides) validateRootConflicts() error {
	for i := range o.rules {
		rule := &o.rules[i]
		if len(rule.Ancestors) > 0 || isOverrideReference(rule.Spec) {
			continue
		}
		rawSpec := o.rootSpecs[rule.Target.Name]
		if rawSpec == "" {
			continue
		}
		spec := o.resolveSpec(rule.Spec)
		if spec != rawSpec {
			return &OverrideConflictError{Name: rule.Target.Name, RawSpec: rawSpec, Spec: spec}
		}
	}
	return nil
}

func (o *Overrides) parseObject(ancestors []OverrideSelector, obj map[string]any) error {
	for key, value := range obj {
		if key == "." {
			if len(ancestors) == 0 {
				return fmt.Errorf("override self rule without package")
			}
			spec, ok := value.(string)
			if !ok {
				return fmt.Errorf("override %q must be a string", key)
			}
			if spec == "" {
				spec = "*"
			}
			target := ancestors[len(ancestors)-1]
			o.addRule(ancestors[:len(ancestors)-1], target, spec)
			continue
		}

		selector := parseOverrideSelector(key)
		if !validPackageName(selector.Name) {
			return &InvalidPackageNameError{Name: selector.Name, Spec: key}
		}
		if selector.Spec != "" && unsupportedSpecClass(selector.Spec) {
			return &UnsupportedSpecError{Name: selector.Name, Spec: selector.Spec, Type: "override"}
		}
		switch typed := value.(type) {
		case string:
			if typed == "" {
				typed = "*"
			}
			if !isOverrideReference(typed) && unsupportedSpecClass(typed) {
				return &UnsupportedSpecError{Name: selector.Name, Spec: typed, Type: "override"}
			}
			o.addRule(ancestors, selector, typed)
		case map[string]any:
			if len(typed) == 0 {
				o.addRule(ancestors, selector, "*")
				continue
			}
			if self, ok := typed["."]; ok {
				spec, ok := self.(string)
				if !ok {
					return fmt.Errorf("override %q self rule must be a string", key)
				}
				if spec == "" {
					spec = "*"
				}
				if !isOverrideReference(spec) && unsupportedSpecClass(spec) {
					return &UnsupportedSpecError{Name: selector.Name, Spec: spec, Type: "override"}
				}
				o.addRule(ancestors, selector, spec)
			}
			childAncestors := appendSelector(ancestors, selector)
			children := map[string]any{}
			for childKey, childValue := range typed {
				if childKey != "." {
					children[childKey] = childValue
				}
			}
			if len(children) > 0 {
				if err := o.parseObject(childAncestors, children); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("override %q must be a string or object", key)
		}
	}
	return nil
}

func (o *Overrides) addRule(ancestors []OverrideSelector, target OverrideSelector, spec string) {
	if target.Name == "" {
		return
	}
	o.rules = append(o.rules, OverrideRule{
		Ancestors: append([]OverrideSelector(nil), ancestors...),
		Target:    target,
		Spec:      spec,
	})
}

func (o *Overrides) Resolve(parent *Node, name, spec string) string {
	resolved, _ := o.ResolveWithRule(parent, name, spec)
	return resolved
}

func (o *Overrides) ResolveWithRule(parent *Node, name, spec string) (string, *OverrideRule) {
	if o == nil {
		return "", nil
	}
	var best *OverrideRule
	for i := range o.rules {
		rule := &o.rules[i]
		if rule.Target.Name != name {
			continue
		}
		if rule.Target.Spec != "" && !selectorSpecMatches(spec, rule.Target.Spec) {
			continue
		}
		if !matchAncestors(parent, rule.Ancestors) {
			continue
		}
		if best == nil || moreSpecificOverride(rule, best) {
			best = rule
		}
	}
	if best == nil {
		return "", nil
	}
	return o.resolveSpec(best.Spec), best
}

func moreSpecificOverride(candidate, current *OverrideRule) bool {
	if len(candidate.Ancestors) != len(current.Ancestors) {
		return len(candidate.Ancestors) > len(current.Ancestors)
	}
	candidateScore := overrideSpecificity(candidate)
	currentScore := overrideSpecificity(current)
	if candidateScore != currentScore {
		return candidateScore > currentScore
	}
	return false
}

func overrideSpecificity(rule *OverrideRule) int {
	score := 0
	if rule.Target.Spec != "" {
		score += 2
	}
	for _, ancestor := range rule.Ancestors {
		if ancestor.Spec != "" {
			score++
		}
	}
	return score
}

func (o *Overrides) resolveSpec(spec string) string {
	if !strings.HasPrefix(spec, "$") {
		return spec
	}
	ref := strings.TrimPrefix(spec, "$")
	if ref == "" {
		return spec
	}
	if value := o.rootSpecs[ref]; value != "" {
		return value
	}
	return spec
}

func isOverrideReference(spec string) bool {
	return strings.HasPrefix(spec, "$")
}

func parseOverrideSelector(key string) OverrideSelector {
	name, spec := splitNameSpec(key)
	return OverrideSelector{Name: name, Spec: spec}
}

func appendSelector(in []OverrideSelector, selector OverrideSelector) []OverrideSelector {
	out := append([]OverrideSelector(nil), in...)
	return append(out, selector)
}

func selectorSpecMatches(candidate, selector string) bool {
	if selector == "" {
		return true
	}
	if candidate == selector {
		return true
	}
	if rangeIntersects(candidate, selector) {
		return true
	}
	return satisfies(candidate, selector)
}

func matchAncestors(parent *Node, ancestors []OverrideSelector) bool {
	if len(ancestors) == 0 {
		return true
	}
	cursor := parent
	for i := len(ancestors) - 1; i >= 0; i-- {
		selector := ancestors[i]
		matched := false
		for cursor != nil && cursor.ID != "root" {
			if cursor.Name == selector.Name && (selector.Spec == "" || satisfies(cursor.Version, selector.Spec)) {
				matched = true
				cursor = cursor.Parent
				break
			}
			cursor = cursor.Parent
		}
		if !matched {
			return false
		}
	}
	return true
}
