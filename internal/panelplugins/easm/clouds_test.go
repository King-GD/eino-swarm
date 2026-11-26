package easm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// cloudCaller answers the provider distribution, the per-provider service
// lookups and the other stats consumers of one run. Service lookups run
// concurrently, so the recorded requests are guarded.
type cloudCaller struct {
	distribution string
	services     func(query string) (string, error)
	mu           sync.Mutex
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
				c.mu.Lock()
				c.sent = append(c.sent, query)
				c.sizes = append(c.sizes, arguments["size"])
				c.mu.Unlock()
				return c.services(query)
			case "ip":
				return `{"size":1,"distinct":{"ip":1}}`, nil
			default:
				return `{"aggs":{"product":[]}}`, nil
			}
		},
	}}
}

// cloudServiceReply echoes the filtered query, because a reply describing
// another range is refused.
func cloudServiceReply(query, products string) (string, error) {
	echoed, err := json.Marshal(query)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"query":%s,"aggs":{"product":%s}}`, echoed, products), nil
}

// Spellings of one provider become one row with their summed asset counts and
// the filtered query's distinct IP count. Tencent and Tencent Mail stay apart
// because one is compute and the other is mail. Services are shown as reported.
func TestCloudsMergeSpellingsIntoOneRowPerProvider(t *testing.T) {
	distribution := `{"query":"org=\"Subject\"","aggs":{"cloud_name":[
		{"name":"BaiShanCloud","count":1131,"uniq":{"ip":130}},
		{"name":"BaishanCloud","count":4,"uniq":{"ip":1}},
		{"name":"Tencent","count":278,"uniq":{"ip":90}},
		{"name":"Tencent Mail","count":56,"uniq":{"ip":40}},
		{"name":"Cloudfront","count":25,"uniq":{"ip":20}},
		{"name":"cloudfront","count":1,"uniq":{"ip":1}},
		{"name":"Aliyun","count":26,"uniq":{"ip":20}}]}}`
	caller := &cloudCaller{distribution: distribution, services: func(query string) (string, error) {
		switch {
		case strings.Contains(query, "BaiShanCloud"):
			return fmt.Sprintf(`{"query":%q,"distinct":{"ip":130},"aggs":{"product":[{"name":"NGINX","count":980},{"name":"白山云科技-白山云","count":36},{"name":"OpenResty","count":54}]}}`, query), nil
		case strings.Contains(query, "Tencent Mail"):
			return cloudServiceReply(query, `[{"name":"Exchange","count":30}]`)
		case strings.Contains(query, `"Tencent"`):
			return cloudServiceReply(query, `[{"name":"OpenResty","count":200}]`)
		case strings.Contains(query, "Aliyun"):
			return cloudServiceReply(query, `[{"name":"Tengine","count":9}]`)
		default:
			return fmt.Sprintf(`{"query":%q,"distinct":{"ip":20},"aggs":{"product":[{"name":"NGINX","count":12}]}}`, query), nil
		}
	}}
	state := runMetrics(t, nil, onlyRoles(RoleStats), caller.caller())
	if got := stepStatus(state, StepClouds).Status; got != statusDone {
		t.Fatalf("clouds step = %+v", stepStatus(state, StepClouds))
	}
	// Equal totals break on the merge key, so two runs over one reply order the
	// same way.
	want := []Cloud{
		{Name: "BaiShanCloud", DisplayName: "白山云", Category: "cdn", Assets: 1135, IPs: 130, Services: []CloudService{{"NGINX", 980}, {"OpenResty", 54}, {"白山云科技-白山云", 36}}},
		{Name: "Tencent", DisplayName: "腾讯云", Category: "cloud", Assets: 278, IPs: 90, Services: []CloudService{{"OpenResty", 200}}},
		{Name: "Tencent Mail", DisplayName: "腾讯企业邮箱", Category: "mail", Assets: 56, IPs: 40, Services: []CloudService{{"Exchange", 30}}},
		{Name: "Cloudfront", DisplayName: "CloudFront", Category: "cdn", Assets: 26, IPs: 20, Services: []CloudService{{"NGINX", 12}}},
		{Name: "Aliyun", DisplayName: "阿里云", Category: "cloud", Assets: 26, IPs: 20, Services: []CloudService{{"Tengine", 9}}},
	}
	if !reflect.DeepEqual(state.Clouds, want) {
		t.Fatalf("clouds = %+v", state.Clouds)
	}
	// Distinct spellings each become their own term; case variants collapse.
	if len(caller.sent) != len(want) || !containsString(caller.sent, `(org="Subject") && (cloud_name="BaiShanCloud")`) {
		t.Fatalf("service requests = %v", caller.sent)
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

// max_services can be configured below the five buckets the stats API accepts,
// so the request asks for the floor and the row keeps only the configured
// number: sending the configured value would fail every lookup.
func TestCloudsRequestTheBackendFloorAndTrimServices(t *testing.T) {
	caller := &cloudCaller{
		distribution: `{"query":"org=\"Subject\"","aggs":{"cloud_name":[{"name":"AWS","count":10,"uniq":{"ip":4}}]}}`,
		services: func(query string) (string, error) {
			return cloudServiceReply(query, `[{"name":"NGINX","count":6},{"name":"Envoy","count":5},{"name":"Apache","count":4},{"name":"Caddy","count":3},{"name":"HAProxy","count":2}]`)
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

// The table shows the largest max_clouds providers. A repeated service bucket
// is shown once, equal counts order by name so two runs agree, and a reply
// with an empty product list leaves the row's services empty.
func TestCloudsKeepTheLargestProvidersAndOrderServicesStably(t *testing.T) {
	caller := &cloudCaller{
		distribution: `{"query":"org=\"Subject\"","aggs":{"cloud_name":[{"name":"AWS","count":10,"uniq":{"ip":4}},{"name":"Aliyun","count":5,"uniq":{"ip":2}},{"name":"Fastly","count":1,"uniq":{"ip":1}}]}}`,
		services: func(query string) (string, error) {
			if strings.Contains(query, "Aliyun") {
				return cloudServiceReply(query, `[]`)
			}
			return cloudServiceReply(query, `[{"name":"Zeus","count":3},{"name":"Apache","count":3},{"name":"Zeus","count":3}]`)
		},
	}
	settings := onlyRoles(RoleStats)
	settings.MaxClouds = 2
	state := runMetrics(t, nil, settings, caller.caller())
	want := []Cloud{
		{Name: "AWS", DisplayName: "AWS", Category: "cloud", Assets: 10, IPs: 4, Services: []CloudService{{"Apache", 3}, {"Zeus", 3}}},
		{Name: "Aliyun", DisplayName: "阿里云", Category: "cloud", Assets: 5, IPs: 2, Services: []CloudService{}},
	}
	if !reflect.DeepEqual(state.Clouds, want) || len(caller.sent) != 2 {
		t.Fatalf("clouds = %+v, requests = %v", state.Clouds, caller.sent)
	}
}

// A missing product list leaves the counts usable while keeping the service
// lookup eligible for another collection. An explicit empty list is complete.
func TestCloudsRetryMissingServiceAggregation(t *testing.T) {
	settings := onlyRoles(RoleStats)
	input := Input{Subject: "Subject", Query: metricQuery}
	distribution := `{"aggs":{"cloud_name":[{"name":"AWS","count":7,"uniq":{"ip":3}}]}}`
	wantService := []CloudService{{"NGINX", 5}}
	for _, test := range []struct {
		name, reply string
		wantRetry   bool
	}{
		{"empty object", `{}`, true},
		{"null reply", `null`, true},
		{"null aggregation", `{"aggs":null}`, true},
		{"query echo only", `{"query":"(org=\"Subject\") && (cloud_name=\"AWS\")"}`, true},
		{"missing products", `{"aggs":{}}`, true},
		{"null products", `{"aggs":{"product":null}}`, true},
		{"empty products", `{"aggs":{"product":[]}}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			caller := &cloudCaller{distribution: distribution, services: func(string) (string, error) { return test.reply, nil }}
			first, _, err := push(t, nil, input, settings, caller.caller())
			if err != nil {
				t.Fatal(err)
			}
			wantStep := StepStatus{ID: StepClouds, Status: statusDone}
			wantRequests := 1
			wantServices := []CloudService{}
			if test.wantRetry {
				wantStep.Status, wantStep.Code = statusFailed, "malformed_reply"
				wantRequests, wantServices = 2, wantService
			}
			if got := stepStatus(first, StepClouds); got != wantStep {
				t.Fatalf("first clouds step = %+v, want %+v", got, wantStep)
			}
			if len(first.Clouds) != 1 || first.Clouds[0].Assets != 7 || first.Clouds[0].IPs != 3 || len(first.Clouds[0].Services) != 0 {
				t.Fatalf("initial rows = %+v", first.Clouds)
			}
			caller.services = func(query string) (string, error) { return cloudServiceReply(query, `[{"name":"NGINX","count":5}]`) }
			retried, _, err := push(t, stateJSON(t, first), input, settings, caller.caller())
			if err != nil {
				t.Fatal(err)
			}
			if got := stepStatus(retried, StepClouds); got != (StepStatus{ID: StepClouds, Status: statusDone}) || len(caller.sent) != wantRequests {
				t.Fatalf("retried clouds step = %+v, service requests = %v", got, caller.sent)
			}
			first.Clouds[0].Services = wantServices
			if !reflect.DeepEqual(retried.Clouds, first.Clouds) {
				t.Fatalf("retried rows = %+v, want %+v", retried.Clouds, first.Clouds)
			}
		})
	}
}

// The counts come from the distribution, so they are published even when a
// service lookup fails or returns malformed buckets. The step itself fails:
// the empty services are unfinished work, and the next collection repeats them.
func TestCloudsKeepCountsWhenOneServiceLookupFails(t *testing.T) {
	for name, broken := range map[string]func(string) (string, error){
		"tool error":       func(string) (string, error) { return "", errors.New("unreachable") },
		"error envelope":   func(string) (string, error) { return `{"error":true,"errmsg":"limit"}`, nil },
		"another query":    func(string) (string, error) { return `{"query":"other","aggs":{"product":[]}}`, nil },
		"unnamed service":  func(query string) (string, error) { return cloudServiceReply(query, `[{"name":"","count":1}]`) },
		"fractional count": func(query string) (string, error) { return cloudServiceReply(query, `[{"name":"A","count":1.5}]`) },
	} {
		caller := &cloudCaller{
			distribution: `{"query":"org=\"Subject\"","aggs":{"cloud_name":[{"name":"AWS","count":10,"uniq":{"ip":4}},{"name":"Aliyun","count":3,"uniq":{"ip":2}}]}}`,
			services: func(query string) (string, error) {
				if strings.Contains(query, "AWS") {
					return broken(query)
				}
				return cloudServiceReply(query, `[{"name":"Tengine","count":3}]`)
			},
		}
		state := runMetrics(t, nil, onlyRoles(RoleStats), caller.caller())
		if got := stepStatus(state, StepClouds); got.Status != statusFailed {
			t.Fatalf("%s: clouds step = %+v", name, got)
		}
		want := []Cloud{
			{Name: "AWS", DisplayName: "AWS", Category: "cloud", Assets: 10, IPs: 4, Services: []CloudService{}},
			{Name: "Aliyun", DisplayName: "阿里云", Category: "cloud", Assets: 3, IPs: 2, Services: []CloudService{{"Tengine", 3}}},
		}
		if !reflect.DeepEqual(state.Clouds, want) {
			t.Fatalf("%s: clouds = %+v", name, state.Clouds)
		}
	}
}

// The distribution carries every row's counts. A bucket without a name, an
// exact count or its distinct IP count cannot become a row, so the step fails
// instead of publishing a table with invented numbers.
func TestCloudsRefuseBucketsThatCannotBeShown(t *testing.T) {
	for name, distribution := range map[string]string{
		"unnamed bucket":   `{"aggs":{"cloud_name":[{"name":"","count":1,"uniq":{"ip":1}}]}}`,
		"fractional count": `{"aggs":{"cloud_name":[{"name":"AWS","count":1.5,"uniq":{"ip":1}}]}}`,
		"negative count":   `{"aggs":{"cloud_name":[{"name":"AWS","count":-1,"uniq":{"ip":1}}]}}`,
		"missing ip count": `{"aggs":{"cloud_name":[{"name":"AWS","count":1}]}}`,
		"fractional ips":   `{"aggs":{"cloud_name":[{"name":"AWS","count":1,"uniq":{"ip":0.5}}]}}`,
		"missing buckets":  `{"aggs":{}}`,
		"null buckets":     `{"aggs":{"cloud_name":null}}`,
		"another query":    `{"query":"other","aggs":{"cloud_name":[]}}`,
	} {
		state := runMetrics(t, nil, onlyRoles(RoleStats), (&cloudCaller{
			distribution: distribution,
			services:     func(query string) (string, error) { return cloudServiceReply(query, `[]`) },
		}).caller())
		if got := stepStatus(state, StepClouds); got.Status != statusFailed || got.Code != "malformed_reply" {
			t.Fatalf("%s: clouds step = %+v", name, got)
		}
	}
}

// An unfilterable provider can keep a single bucket's confirmed count. The
// same name across two buckets cannot publish a count without the filter.
func TestCloudsShowOnlyConfirmedCountsForUnfilterableProviders(t *testing.T) {
	for _, test := range []struct {
		name, additionalBucket string
		wantRows               int
	}{
		{"single bucket", "", 1},
		{"merged buckets", `,{"name":"A\"B","count":1,"uniq":{"ip":1}}`, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			caller := &cloudCaller{
				distribution: `{"aggs":{"cloud_name":[{"name":"a\"b","count":2,"uniq":{"ip":1}}` + test.additionalBucket + `]}}`,
				services:     func(query string) (string, error) { return cloudServiceReply(query, `[{"name":"NGINX","count":2}]`) },
			}
			state := runMetrics(t, nil, onlyRoles(RoleStats), caller.caller())
			if got := stepStatus(state, StepClouds); got.Status != statusFailed {
				t.Fatalf("clouds step = %+v", got)
			}
			if len(caller.sent) != 0 || len(state.Clouds) != test.wantRows {
				t.Fatalf("requests = %v, clouds = %+v", caller.sent, state.Clouds)
			}
			if test.wantRows == 1 && (state.Clouds[0].Assets != 2 || state.Clouds[0].IPs != 1 || len(state.Clouds[0].Services) != 0) {
				t.Fatalf("clouds = %+v", state.Clouds)
			}
		})
	}
}

// The highest-count providers share the in-flight request budget with the
// other steps.
func TestPanelOnlyLooksUpTheFiveLargestCloudsWithinTheSharedBudget(t *testing.T) {
	var buckets []string
	for i := range 20 {
		buckets = append(buckets, fmt.Sprintf(`{"name":"Provider %02d","count":%d,"uniq":{"ip":1}}`, i, i+1))
	}
	distribution := `{"query":"org=\"Subject\"","aggs":{"cloud_name":[` + strings.Join(buckets, ",") + `]}}`
	var mu sync.Mutex
	inFlight, peak := 0, 0
	caller := &cloudCaller{distribution: distribution, services: func(query string) (string, error) {
		time.Sleep(5 * time.Millisecond)
		return cloudServiceReply(query, `[]`)
	}}
	base := caller.caller()
	tracked := CallerFunc(func(ctx context.Context, tool string, arguments map[string]any) (json.RawMessage, error) {
		mu.Lock()
		inFlight++
		peak = max(peak, inFlight)
		mu.Unlock()
		defer func() {
			mu.Lock()
			inFlight--
			mu.Unlock()
		}()
		return base.Call(ctx, tool, arguments)
	})
	state := runMetrics(t, nil, testSettings(), tracked)
	if got := stepStatus(state, StepClouds); got.Status != statusDone || len(state.Clouds) != 5 {
		t.Fatalf("clouds step = %+v, rows = %d", got, len(state.Clouds))
	}
	if len(caller.sent) != 5 || peak > panelRequestSlots || peak < 2 {
		t.Fatalf("requests = %d, peak concurrency = %d", len(caller.sent), peak)
	}
	// The distribution arrives smallest-first and requests finish concurrently.
	for i, cloud := range state.Clouds {
		wantName := fmt.Sprintf("Provider %02d", 19-i)
		wantQuery := fmt.Sprintf(`(%s) && (cloud_name="%s")`, metricQuery, wantName)
		if cloud.Name != wantName || !containsString(caller.sent, wantQuery) {
			t.Fatalf("row %d = %s, service requests = %v", i, cloud.Name, caller.sent)
		}
	}
}

// Stop while service lookups are in flight must not publish a table whose
// services were cut short as if it were complete: the step ends interrupted.
func TestStopDuringServiceLookupsInterruptsTheStep(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	fallback := make(chan struct{}, 1)
	var once sync.Once
	caller := CallerFunc(func(callCtx context.Context, _ string, arguments map[string]any) (json.RawMessage, error) {
		switch arguments["fields"] {
		case "cloud_name":
			return json.RawMessage(`{"aggs":{"cloud_name":[{"name":"Microsoft Azure","count":1,"uniq":{"ip":1}},{"name":"azure","count":1,"uniq":{"ip":1}}]}}`), nil
		case "product3":
			once.Do(func() { close(started) })
			<-callCtx.Done()
			return nil, callCtx.Err()
		case "ip":
			if arguments["query"] != metricQuery {
				select {
				case fallback <- struct{}{}:
				default:
				}
			}
		}
		return nil, ErrUnavailable
	})
	var out published
	returned := make(chan error, 1)
	go func() {
		_, err := Run(ctx, Input{Subject: "Subject", Query: metricQuery}, onlyRoles(RoleStats), nil, caller, out.publish)
		returned <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("service lookup never started")
	}
	cancel()
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned %v, want cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after Stop")
	}
	last := out.last()
	if got := stepStatus(last, StepClouds); got.Status != statusInterrupted {
		t.Fatalf("clouds step = %+v", got)
	}
	if len(last.Clouds) != 0 {
		t.Fatalf("an interrupted table was published: %+v", last.Clouds)
	}
	if len(fallback) != 0 {
		t.Fatal("Stop still sent a filtered IP lookup")
	}
}

// A query with no tagged assets still finishes the step: the table is empty
// because the source had nothing to report, not because collection failed.
func TestCloudsAcceptAnEmptyDistribution(t *testing.T) {
	caller := &cloudCaller{
		distribution: `{"query":"org=\"Subject\"","aggs":{"cloud_name":[]}}`,
		services:     func(query string) (string, error) { return cloudServiceReply(query, `[]`) },
	}
	state := runMetrics(t, nil, onlyRoles(RoleStats), caller.caller())
	if got := stepStatus(state, StepClouds); got.Status != statusDone {
		t.Fatalf("clouds step = %+v", got)
	}
	if state.Clouds == nil || len(state.Clouds) != 0 || len(caller.sent) != 0 {
		t.Fatalf("clouds = %+v, requests = %v", state.Clouds, caller.sent)
	}
}

// Continuing a portrait keeps the rows collected for the same query, and a new
// query drops them rather than showing two asset ranges in one table.
func TestCloudsFollowTheQueryTheyWereCollectedFor(t *testing.T) {
	stats := (&cloudCaller{
		distribution: `{"query":"org=\"Subject\"","aggs":{"cloud_name":[{"name":"AWS","count":10,"uniq":{"ip":4}}]}}`,
		services:     func(query string) (string, error) { return cloudServiceReply(query, `[{"name":"NGINX","count":6}]`) },
	}).caller()
	stats.replies[RoleHeat] = fixed(`{"hot_trend":[{"date":"2026-01-02","value":1}]}`)
	stats.replies[RoleProductRules] = fixed(`{"results":[]}`)
	var out published
	if _, err := Run(t.Context(), Input{Subject: "Subject", Query: metricQuery}, testSettings(), nil, stats, out.publish); err != nil {
		t.Fatal(err)
	}
	current := stateJSON(t, out.last())
	if len(out.last().Clouds) != 1 || out.last().Collection.Status != statusDone {
		t.Fatalf("clouds = %+v, collection = %s", out.last().Clouds, out.last().Collection.Status)
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

// A request the backend refuses as rate limited is repeated once after a pause.
// The pause must outlast the backend's one-second window, and a second refusal
// fails the step instead of looping.
func TestRateLimitedRequestIsRetriedOnce(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	base := aggregationReplies(`{"size":7,"distinct":{"ip":3}}`, `{"hot_trend":[{"date":"2026-01-02","value":2}]}`)
	limited := CallerFunc(func(ctx context.Context, tool string, arguments map[string]any) (json.RawMessage, error) {
		if arguments["fields"] == "ip" {
			mu.Lock()
			attempts++
			attempt := attempts
			mu.Unlock()
			if attempt == 1 {
				return nil, &ToolError{Code: rateLimitedCode}
			}
		}
		return base.Call(ctx, tool, arguments)
	})
	started := time.Now()
	state := runMetrics(t, nil, testSettings(), limited)
	elapsed := time.Since(started)
	if attempts != 2 {
		t.Fatalf("summary attempts = %d, want the first refusal and one repeat", attempts)
	}
	if elapsed < rateLimitRetryWait {
		t.Fatalf("retried after %s, shorter than the backend window", elapsed)
	}
	if got := stepStatus(state, StepSummary); got.Status != statusDone || state.Summary == nil || state.Summary.FOFAAssets == nil || *state.Summary.FOFAAssets != 7 {
		t.Fatalf("summary step = %+v, summary = %+v", got, state.Summary)
	}

	refused := 0
	always := CallerFunc(func(ctx context.Context, tool string, arguments map[string]any) (json.RawMessage, error) {
		if arguments["fields"] == "ip" {
			refused++
			return nil, &ToolError{Code: rateLimitedCode}
		}
		return base.Call(ctx, tool, arguments)
	})
	failed := runMetrics(t, nil, onlyRoles(RoleStats), always)
	if refused != 2 {
		t.Fatalf("refused requests = %d, want one repeat and then a stop", refused)
	}
	if got := stepStatus(failed, StepSummary); got.Status != statusFailed || got.Code != rateLimitedCode {
		t.Fatalf("summary step = %+v", got)
	}
}
