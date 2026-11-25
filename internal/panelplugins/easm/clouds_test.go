package easm

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// cloudCaller answers the distribution, the per-provider detail and the other
// stats consumers of one run, so a test sees the requests a real step sends.
type cloudCaller struct {
	distribution string
	detail       func(query string) (string, error)
	sent         []string
	sizes        []any
}

func (c *cloudCaller) caller() *fakeCaller {
	return &fakeCaller{replies: metricReplies{
		RoleStats: func(arguments map[string]any) (string, error) {
			switch arguments["fields"] {
			case "cloud_name":
				return c.distribution, nil
			case "product3":
				query, _ := arguments["query"].(string)
				c.sent = append(c.sent, query)
				c.sizes = append(c.sizes, arguments["size"])
				return c.detail(query)
			case "ip":
				return `{"size":1,"distinct":{"ip":1}}`, nil
			default:
				return `{"aggs":{"product":[]}}`, nil
			}
		},
	}}
}

// cloudDetailReply echoes the filtered query, because a reply describing another
// range is refused.
func cloudDetailReply(query string, assets, ips int64, products string) (string, error) {
	echoed, err := json.Marshal(query)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"query":%s,"size":%d,"distinct":{"ip":%d},"aggs":{"product":%s}}`, echoed, assets, ips, products), nil
}

// The table shows the company's assets behind each provider, not the provider's
// own footprint: each row's totals come from a request filtered to that
// provider. Spellings that name one provider become one row, while Tencent and
// Tencent Mail stay apart because one is compute and the other is mail.
func TestCloudsMergeSpellingsIntoOneRowPerProvider(t *testing.T) {
	distribution := `{"query":"org=\"Subject\"","aggs":{"cloud_name":[
		{"name":"BaiShanCloud","count":1131},
		{"name":"BaishanCloud","count":4},
		{"name":"Tencent","count":278},
		{"name":"Tencent Mail","count":56},
		{"name":"Cloudfront","count":25},
		{"name":"cloudfront","count":1},
		{"name":"Aliyun","count":26}]}}`
	caller := &cloudCaller{distribution: distribution, detail: func(query string) (string, error) {
		switch {
		case strings.Contains(query, "BaiShanCloud"):
			return cloudDetailReply(query, 1135, 131, `[{"name":"NGINX","count":980},{"name":"白山云科技-白山云","count":36},{"name":"OpenResty","count":54},{"name":"金山云-CDN","count":31},{"name":"fasthttp","count":14}]`)
		case strings.Contains(query, "Tencent Mail"):
			return cloudDetailReply(query, 56, 40, `[{"name":"Exchange","count":30}]`)
		case strings.Contains(query, `"Tencent"`):
			return cloudDetailReply(query, 278, 90, `[{"name":"OpenResty","count":200}]`)
		case strings.Contains(query, "Aliyun"):
			return cloudDetailReply(query, 26, 20, `[{"name":"Tengine","count":9}]`)
		default:
			return cloudDetailReply(query, 26, 21, `[{"name":"NGINX","count":12}]`)
		}
	}}
	state := runMetrics(t, nil, onlyRoles(RoleStats), caller.caller())
	if got := stepStatus(state, StepClouds).Status; got != statusDone {
		t.Fatalf("clouds step = %+v", stepStatus(state, StepClouds))
	}
	// Equal totals break on the merge key, so two runs over one reply order the
	// same way.
	want := []Cloud{
		{Name: "BaiShanCloud", DisplayName: "白山云", Category: "cdn", Assets: 1135, IPs: 131, Services: []CloudService{{"NGINX", 980}, {"OpenResty", 54}, {"fasthttp", 14}}},
		{Name: "Tencent", DisplayName: "腾讯云", Category: "cloud", Assets: 278, IPs: 90, Services: []CloudService{{"OpenResty", 200}}},
		{Name: "Tencent Mail", DisplayName: "腾讯企业邮箱", Category: "mail", Assets: 56, IPs: 40, Services: []CloudService{{"Exchange", 30}}},
		{Name: "Cloudfront", DisplayName: "CloudFront", Category: "cdn", Assets: 26, IPs: 21, Services: []CloudService{{"NGINX", 12}}},
		{Name: "Aliyun", DisplayName: "阿里云", Category: "cloud", Assets: 26, IPs: 20, Services: []CloudService{{"Tengine", 9}}},
	}
	if !reflect.DeepEqual(state.Clouds, want) {
		t.Fatalf("clouds = %+v", state.Clouds)
	}
	// Distinct spellings each become their own term, because the producer does
	// not assume the backend treats "aws" and "Amazon AWS" alike. Case variants
	// collapse: the filter matched both spellings of one name in practice, and
	// the total check below would catch it if it did not.
	if len(caller.sent) != len(want) {
		t.Fatalf("detail requests = %v", caller.sent)
	}
	if !containsString(caller.sent, `(org="Subject") && (cloud_name="BaiShanCloud")`) {
		t.Fatalf("merged filter = %v", caller.sent)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// max_services can be configured below the five buckets the stats backend
// accepts, so the request must ask for the floor and the row keeps only the
// configured number: sending the configured value would fail every portrait.
func TestCloudsRequestTheBackendFloorAndTrimServices(t *testing.T) {
	caller := &cloudCaller{
		distribution: `{"query":"org=\"Subject\"","aggs":{"cloud_name":[{"name":"AWS","count":10}]}}`,
		detail: func(query string) (string, error) {
			return cloudDetailReply(query, 10, 4, `[{"name":"NGINX","count":6},{"name":"Envoy","count":5},{"name":"Apache","count":4},{"name":"Caddy","count":3},{"name":"HAProxy","count":2}]`)
		},
	}
	settings := testSettings()
	settings.MaxServices = 2
	state := runMetrics(t, nil, settings, caller.caller())
	if got := stepStatus(state, StepClouds); got.Status != statusDone {
		t.Fatalf("clouds step = %+v", got)
	}
	if len(caller.sizes) != 1 || caller.sizes[0] != 5 {
		t.Fatalf("requested sizes = %v", caller.sizes)
	}
	want := []CloudService{{"NGINX", 6}, {"Envoy", 5}}
	if len(state.Clouds) != 1 || !reflect.DeepEqual(state.Clouds[0].Services, want) {
		t.Fatalf("services = %+v", state.Clouds)
	}
}

// A filtered total that disagrees with the buckets it came from means the filter
// reached a wider asset range than the table shows; publishing it would put two
// different populations in one row.
func TestCloudsRefuseAProviderWhoseFilteredTotalDisagrees(t *testing.T) {
	caller := &cloudCaller{
		distribution: `{"query":"org=\"Subject\"","aggs":{"cloud_name":[{"name":"AWS","count":10}]}}`,
		detail: func(query string) (string, error) {
			return cloudDetailReply(query, 11, 3, `[]`)
		},
	}
	state := runMetrics(t, nil, onlyRoles(RoleStats), caller.caller())
	if got := stepStatus(state, StepClouds); got.Status != statusFailed || got.Code != cloudTotalMismatch {
		t.Fatalf("clouds step = %+v", got)
	}
	if len(state.Clouds) != 0 {
		t.Fatalf("a mismatched row was published: %+v", state.Clouds)
	}
}

// A provider that the source names but the table cannot filter, or a bucket that
// is not an exact count, would leave a row nobody can verify.
func TestCloudsRefuseBucketsThatCannotBeShown(t *testing.T) {
	for name, distribution := range map[string]string{
		"unnamed bucket":    `{"aggs":{"cloud_name":[{"name":"","count":1}]}}`,
		"fractional count":  `{"aggs":{"cloud_name":[{"name":"AWS","count":1.5}]}}`,
		"negative count":    `{"aggs":{"cloud_name":[{"name":"AWS","count":-1}]}}`,
		"missing buckets":   `{"aggs":{}}`,
		"null buckets":      `{"aggs":{"cloud_name":null}}`,
		"another query":     `{"query":"other","aggs":{"cloud_name":[]}}`,
		"unfilterable name": `{"aggs":{"cloud_name":[{"name":"a\"b","count":1}]}}`,
	} {
		state := runMetrics(t, nil, onlyRoles(RoleStats), (&cloudCaller{
			distribution: distribution,
			detail:       func(query string) (string, error) { return cloudDetailReply(query, 1, 1, `[]`) },
		}).caller())
		if got := stepStatus(state, StepClouds); got.Status != statusFailed || got.Code != "malformed_reply" {
			t.Fatalf("%s: clouds step = %+v", name, got)
		}
	}
}

// The detail reply carries the row's numbers; without an exact total or an IP
// count there is nothing to show, and a conflicting duplicate bucket cannot be
// reconciled.
func TestCloudsRefuseMalformedDetailReplies(t *testing.T) {
	valid := `{"query":"org=\"Subject\"","aggs":{"cloud_name":[{"name":"AWS","count":10}]}}`
	for name, reply := range map[string]string{
		"missing total":         `{"distinct":{"ip":3},"aggs":{"product":[]}}`,
		"missing ips":           `{"size":10,"aggs":{"product":[]}}`,
		"fractional total":      `{"size":10.5,"distinct":{"ip":3},"aggs":{"product":[]}}`,
		"unnamed service":       `{"size":10,"distinct":{"ip":3},"aggs":{"product":[{"name":"","count":1}]}}`,
		"conflicting duplicate": `{"size":10,"distinct":{"ip":3},"aggs":{"product":[{"name":"A","count":1},{"name":"A","count":2}]}}`,
	} {
		state := runMetrics(t, nil, onlyRoles(RoleStats), (&cloudCaller{
			distribution: valid,
			detail:       func(string) (string, error) { return reply, nil },
		}).caller())
		if got := stepStatus(state, StepClouds); got.Status != statusFailed || got.Code != "malformed_reply" {
			t.Fatalf("%s: clouds step = %+v", name, got)
		}
	}
}

// A provider with no tagged assets still finishes the step: the table is empty
// because the source had nothing to report, not because collection failed.
func TestCloudsAcceptAnEmptyDistribution(t *testing.T) {
	state := runMetrics(t, nil, onlyRoles(RoleStats), (&cloudCaller{
		distribution: `{"query":"org=\"Subject\"","aggs":{"cloud_name":[]}}`,
		detail:       func(query string) (string, error) { return cloudDetailReply(query, 0, 0, `[]`) },
	}).caller())
	if got := stepStatus(state, StepClouds); got.Status != statusDone {
		t.Fatalf("clouds step = %+v", got)
	}
	if state.Clouds == nil || len(state.Clouds) != 0 {
		t.Fatalf("clouds = %+v", state.Clouds)
	}
}

// Continuing a portrait keeps the rows collected for the same query, and a new
// query drops them rather than showing two asset ranges in one table.
func TestCloudsFollowTheQueryTheyWereCollectedFor(t *testing.T) {
	stats := (&cloudCaller{
		distribution: `{"query":"org=\"Subject\"","aggs":{"cloud_name":[{"name":"AWS","count":10}]}}`,
		detail: func(query string) (string, error) {
			return cloudDetailReply(query, 10, 4, `[{"name":"NGINX","count":6}]`)
		},
	}).caller()
	stats.replies[RoleHeat] = fixed(`{"hot_trend":[{"date":"2026-01-02","value":1}]}`)
	stats.replies[RoleProductRules] = fixed(`{"results":[]}`)
	var out published
	if _, err := Run(t.Context(), Input{Subject: "Subject", Query: metricQuery}, testSettings(), nil, stats, out.publish); err != nil {
		t.Fatal(err)
	}
	current := stateJSON(t, out.last())
	if len(out.last().Clouds) != 1 {
		t.Fatalf("clouds = %+v", out.last().Clouds)
	}
	if got := out.last().Collection.Status; got != statusDone {
		t.Fatalf("collection = %s", got)
	}

	// Every step is done for this query, so the continuation publishes the panel
	// without collecting again.
	continued, _, err := push(t, current, Input{Subject: "Subject", Query: metricQuery}, testSettings(), &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(continued.Clouds, out.last().Clouds) {
		t.Fatalf("continued clouds = %+v", continued.Clouds)
	}

	changed, _, err := push(t, current, Input{Subject: "Subject", Query: `org="Other"`}, onlyRoles(), &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	if changed.Clouds == nil || len(changed.Clouds) != 0 {
		t.Fatalf("clouds survived another query: %+v", changed.Clouds)
	}
}

// A panel stored before the table existed has neither a clouds field nor a
// clouds step. Repeating its query must collect only the providers: the totals,
// trend and components it already holds stay and cost no further requests.
func TestRepeatingQueryOnAPanelStoredBeforeTheTableRunsOnlyClouds(t *testing.T) {
	settings := onlyRoles(RoleStats, RoleHeat)
	input := Input{Subject: "Subject", Query: metricQuery}
	heat := `{"hot_trend":[{"date":"2026-01-02","value":2}]}`
	first, _, err := push(t, nil, input, settings, aggregationReplies(`{"size":7,"distinct":{"ip":3}}`, heat))
	if err != nil {
		t.Fatal(err)
	}

	var stored map[string]any
	if err := json.Unmarshal(stateJSON(t, first), &stored); err != nil {
		t.Fatal(err)
	}
	delete(stored, "clouds")
	collection := stored["collection"].(map[string]any)
	var steps []any
	for _, step := range collection["steps"].([]any) {
		if step.(map[string]any)["id"] != string(StepClouds) {
			steps = append(steps, step)
		}
	}
	collection["steps"] = steps
	legacy, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}

	retry := aggregationReplies(`{"size":7,"distinct":{"ip":3}}`, heat)
	state, _, err := push(t, legacy, input, settings, retry)
	if err != nil {
		t.Fatal(err)
	}
	if got := requestsFor(retry, RoleStats, "cloud_name"); got != 1 || len(retry.requests(RoleStats)) != 1 || len(retry.requests(RoleHeat)) != 0 {
		t.Fatalf("stats requests = %v, heat requests = %v", retry.requests(RoleStats), retry.requests(RoleHeat))
	}
	for _, step := range Steps {
		if got := stepStatus(state, step); got.Status != statusDone {
			t.Errorf("%s = %+v", step, got)
		}
	}
	if state.Summary == nil || state.Summary.FOFAAssets == nil || *state.Summary.FOFAAssets != 7 || len(state.Heat) != 1 || len(state.Components) != 1 {
		t.Fatalf("retained results = summary %+v, heat %+v, components %+v", state.Summary, state.Heat, state.Components)
	}
	if state.Clouds == nil {
		t.Fatal("clouds were not published as a list")
	}
}
