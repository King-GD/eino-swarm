package easm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// componentLayers limits the supply chain to hardware and applications.
const componentLayers = "product1,product5"

func init() {
	stepFuncs[StepComponents] = stepDef{roles: []string{RoleStats}, fn: (*run).components}
}

// components aggregates the query's assets by product. FOFA may answer with a
// numeric rule ID instead of a product name; the ID stays the component's
// stable name and, when a product-rule tool is configured, its rule record
// supplies the display name.
func (r *run) components(ctx context.Context) error {
	var reply struct {
		Query json.RawMessage `json:"query"`
		Aggs  struct {
			Product *[]struct {
				Name  json.RawMessage `json:"name"`
				Count json.RawMessage `json:"count"`
			} `json:"product"`
		} `json:"aggs"`
	}
	// FOFA merges the two requested layers into one "product" bucket list.
	arguments := map[string]any{"query": r.input.Query, "fields": componentLayers, "tool": fofaSource, "size": r.settings.MaxComponents}
	if err := r.call(ctx, RoleStats, arguments, &reply); err != nil {
		return err
	}
	if err := r.sameQuery(reply.Query); err != nil {
		return err
	}
	if reply.Aggs.Product == nil {
		return fmt.Errorf("reply carries no product buckets")
	}
	components := make([]Component, 0, len(*reply.Aggs.Product))
	seen := map[string]int64{}
	var ruleIDs []string
	for _, bucket := range *reply.Aggs.Product {
		name, ruleID := productName(bucket.Name)
		count, ok, err := exactCount(bucket.Count)
		if name == "" || err != nil || !ok {
			return fmt.Errorf("malformed product bucket")
		}
		// FOFA can repeat a bucket across the two product fields; the same
		// count is one product, a different one cannot be reconciled.
		if previous, duplicate := seen[name]; duplicate {
			if previous != count {
				return fmt.Errorf("conflicting product bucket")
			}
			continue
		}
		seen[name] = count
		components = append(components, Component{Name: name, Count: count})
		if ruleID {
			ruleIDs = append(ruleIDs, name)
		}
	}
	r.state.Components = components
	if len(ruleIDs) > 0 && r.settings.Tools[RoleProductRules] != "" {
		labels := r.productLabels(ctx, ruleIDs)
		for i := range r.state.Components {
			r.state.Components[i].DisplayName = labels[r.state.Components[i].Name]
		}
	}
	return nil
}

// productName returns a bucket's product name and whether it is a numeric
// rule ID. A rounded or negative number is not an identity and yields "".
func productName(raw json.RawMessage) (string, bool) {
	var name string
	if json.Unmarshal(raw, &name) == nil {
		return strings.TrimSpace(name), false
	}
	if id := decimalID(raw); id != "" {
		return id, true
	}
	return "", false
}

// productLabels resolves rule IDs to product names. Labels only decorate
// components already collected, so a failed lookup leaves the IDs shown
// rather than failing the step; cancellation is still seen by Run.
func (r *run) productLabels(ctx context.Context, ids []string) map[string]string {
	requested := make(map[string]bool, len(ids))
	numbers := make([]json.Number, len(ids))
	for i, id := range ids {
		requested[id] = true
		numbers[i] = json.Number(id)
	}
	var reply struct {
		Results []struct {
			RuleID    json.RawMessage `json:"rule_id"`
			Product   string          `json:"product"`
			EnProduct string          `json:"en_product"`
		} `json:"results"`
	}
	if r.call(ctx, RoleProductRules, map[string]any{"rule_ids": numbers}, &reply) != nil {
		return nil
	}
	labels := map[string]string{}
	conflicting := map[string]bool{}
	for _, rule := range reply.Results {
		id := decimalID(rule.RuleID)
		label := strings.TrimSpace(rule.Product)
		if label == "" {
			label = strings.TrimSpace(rule.EnProduct)
		}
		if !requested[id] || label == "" {
			continue
		}
		// Two names for one rule cannot both be right; show the ID instead.
		if previous, found := labels[id]; found && previous != label {
			conflicting[id] = true
		}
		labels[id] = label
	}
	for id := range conflicting {
		delete(labels, id)
	}
	return labels
}
