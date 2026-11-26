package easm

import (
	"fmt"
	"reflect"
	"testing"
)

const azureAliasFilter = `cloud_name="Microsoft Azure" || cloud_name="azure"`

func TestCloudAliasesReuseTheServiceReplyForDistinctIPs(t *testing.T) {
	for _, test := range []struct {
		name, secondAlias, filter string
		ips                       int64
	}{
		{"different spellings", "azure", azureAliasFilter, 1},
		{"case-only spellings", "MICROSOFT AZURE", `cloud_name="Microsoft Azure"`, 1},
		{"confirmed zero IPs", "azure", azureAliasFilter, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			reply := fmt.Sprintf(`{"distinct":{"ip":%d},"aggs":{"product":[{"name":"NGINX","count":2}]}}`, test.ips)
			caller := azureCloudCaller(test.secondAlias, reply, `{}`)
			state := runMetrics(t, nil, onlyRoles(RoleStats), caller)
			if got := stepStatus(state, StepClouds); got.Status != statusDone {
				t.Fatalf("clouds step = %+v", got)
			}
			want := []Cloud{{Name: "Microsoft Azure", DisplayName: "微软 Azure", Category: "cloud", Assets: 2, IPs: test.ips, Services: []CloudService{{"NGINX", 2}}}}
			if !reflect.DeepEqual(state.Clouds, want) {
				t.Fatalf("clouds = %+v, want %+v", state.Clouds, want)
			}
			assertAzureCloudRequests(t, caller, test.filter, []string{"product3"})
			if got := len(caller.requests(RoleStats)); got != 4 {
				t.Fatalf("stats requests = %d, want summary, components, distribution and service requests only", got)
			}
		})
	}
}

func TestCloudAliasIPsSurviveAnIncompleteServiceReply(t *testing.T) {
	for _, test := range []struct{ name, reply string }{
		{"missing products", `{"distinct":{"ip":1},"aggs":{}}`},
		{"malformed aggregation", `{"distinct":{"ip":1},"aggs":true}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			first := azureCloudCaller("azure", test.reply, `{}`)
			saved := runMetrics(t, nil, onlyRoles(RoleStats), first)
			if got := stepStatus(saved, StepClouds); got != (StepStatus{ID: StepClouds, Status: statusFailed, Code: "malformed_reply"}) {
				t.Fatalf("clouds step = %+v", got)
			}
			if len(saved.Clouds) != 1 || saved.Clouds[0].Assets != 2 || saved.Clouds[0].IPs != 1 || len(saved.Clouds[0].Services) != 0 {
				t.Fatalf("saved clouds = %+v", saved.Clouds)
			}
			assertAzureCloudRequests(t, first, azureAliasFilter, []string{"product3"})

			retry := azureCloudCaller("azure", `{"distinct":{"ip":1},"aggs":{"product":[{"name":"NGINX","count":2}]}}`, `{}`)
			state := runMetrics(t, stateJSON(t, saved), onlyRoles(RoleStats), retry)
			if got := stepStatus(state, StepClouds); got.Status != statusDone {
				t.Fatalf("retried clouds step = %+v", got)
			}
			saved.Clouds[0].Services = []CloudService{{"NGINX", 2}}
			if !reflect.DeepEqual(state.Clouds, saved.Clouds) {
				t.Fatalf("retried clouds = %+v, want %+v", state.Clouds, saved.Clouds)
			}
			assertAzureCloudRequests(t, retry, azureAliasFilter, []string{"product3"})
			if got := len(retry.requests(RoleStats)); got != 2 {
				t.Fatalf("retry stats requests = %d, want distribution and service requests only", got)
			}
		})
	}
}

func TestCloudAliasesRequestAFilteredIPFallbackOnlyWhenNeeded(t *testing.T) {
	for _, test := range []struct{ name, distinct string }{
		{"missing", ""},
		{"null", `,"distinct":{"ip":null}`},
		{"fractional", `,"distinct":{"ip":0.5}`},
		{"wrong type", `,"distinct":true`},
	} {
		t.Run(test.name, func(t *testing.T) {
			serviceReply := `{"aggs":{"product":[{"name":"NGINX","count":2}]}` + test.distinct + `}`
			caller := azureCloudCaller("azure", serviceReply, `{"distinct":{"ip":1}}`)
			state := runMetrics(t, nil, onlyRoles(RoleStats), caller)
			if got := stepStatus(state, StepClouds); got.Status != statusDone {
				t.Fatalf("clouds step = %+v", got)
			}
			if len(state.Clouds) != 1 || state.Clouds[0].Assets != 2 || state.Clouds[0].IPs != 1 || !reflect.DeepEqual(state.Clouds[0].Services, []CloudService{{"NGINX", 2}}) {
				t.Fatalf("clouds = %+v", state.Clouds)
			}
			assertAzureCloudRequests(t, caller, azureAliasFilter, []string{"product3", "ip"})
		})
	}
}

func TestCloudAliasesWaitForAConfirmedIPCountBeforePublishing(t *testing.T) {
	const serviceReply = `{"aggs":{"product":[{"name":"NGINX","count":2}]}}`
	// A size or a count tied to a different query cannot settle this row.
	for _, test := range []struct{ name, reply string }{
		{"only size", `{"size":1}`},
		{"wrong query", `{"query":"other","distinct":{"ip":1}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			first := azureCloudCaller("azure", serviceReply, test.reply)
			saved := runMetrics(t, nil, onlyRoles(RoleStats), first)
			if got := stepStatus(saved, StepClouds); got != (StepStatus{ID: StepClouds, Status: statusFailed, Code: "malformed_reply"}) || len(saved.Clouds) != 0 {
				t.Fatalf("clouds step = %+v, rows = %+v", got, saved.Clouds)
			}
			assertAzureCloudRequests(t, first, azureAliasFilter, []string{"product3", "ip"})

			retry := azureCloudCaller("azure", serviceReply, `{"distinct":{"ip":1}}`)
			state := runMetrics(t, stateJSON(t, saved), onlyRoles(RoleStats), retry)
			if got := stepStatus(state, StepClouds); got.Status != statusDone || len(state.Clouds) != 1 || state.Clouds[0].IPs != 1 {
				t.Fatalf("retried clouds step = %+v, rows = %+v", got, state.Clouds)
			}
			assertAzureCloudRequests(t, retry, azureAliasFilter, []string{"product3", "ip"})
		})
	}
}

func TestCloudAliasFallbackFailureKeepsTheOtherProvider(t *testing.T) {
	caller := azureCloudCaller("azure", `{"aggs":{"product":[]}}`, `{}`)
	azureReplies := caller.replies[RoleStats]
	awsQuery := "(" + metricQuery + `) && (cloud_name="AWS")`
	caller.replies[RoleStats] = func(arguments map[string]any) (string, error) {
		switch {
		case arguments["fields"] == "cloud_name":
			return `{"aggs":{"cloud_name":[{"name":"Microsoft Azure","count":1,"uniq":{"ip":1}},{"name":"azure","count":1,"uniq":{"ip":1}},{"name":"AWS","count":1,"uniq":{"ip":1}}]}}`, nil
		case arguments["fields"] == "ip" && arguments["query"] != metricQuery:
			return "", &ToolError{Code: "tool_error"}
		case arguments["query"] == awsQuery:
			return `{"distinct":{"ip":9},"aggs":{"product":[{"name":"NGINX","count":1}]}}`, nil
		default:
			return azureReplies(arguments)
		}
	}
	state := runMetrics(t, nil, onlyRoles(RoleStats), caller)
	if got := stepStatus(state, StepClouds); got != (StepStatus{ID: StepClouds, Status: statusFailed, Code: "tool_error"}) {
		t.Fatalf("clouds step = %+v", got)
	}
	want := []Cloud{{Name: "AWS", DisplayName: "AWS", Category: "cloud", Assets: 1, IPs: 1, Services: []CloudService{{"NGINX", 1}}}}
	if !reflect.DeepEqual(state.Clouds, want) {
		t.Fatalf("clouds = %+v, want %+v", state.Clouds, want)
	}
	if got := len(caller.requests(RoleStats)); got != 6 {
		t.Fatalf("stats requests = %d, want two service requests and only one IP fallback", got)
	}
}

func azureCloudCaller(secondAlias, serviceReply, ipReply string) *fakeCaller {
	distribution := fmt.Sprintf(`{"aggs":{"cloud_name":[{"name":"Microsoft Azure","count":1,"uniq":{"ip":1}},{"name":%q,"count":1,"uniq":{"ip":1}}]}}`, secondAlias)
	return &fakeCaller{replies: metricReplies{
		RoleStats: func(arguments map[string]any) (string, error) {
			switch arguments["fields"] {
			case "cloud_name":
				return distribution, nil
			case "product3":
				return serviceReply, nil
			case "ip":
				if arguments["query"] == metricQuery {
					return `{"size":2,"distinct":{"ip":1}}`, nil
				}
				return ipReply, nil
			default:
				return `{"aggs":{"product":[]}}`, nil
			}
		},
	}}
}

func assertAzureCloudRequests(t *testing.T, caller *fakeCaller, filter string, want []string) {
	t.Helper()
	query := "(" + metricQuery + ") && (" + filter + ")"
	var got []string
	for _, request := range caller.requests(RoleStats) {
		if request["query"] == metricQuery {
			continue
		}
		if request["query"] != query || request["tool"] != fofaSource {
			t.Fatalf("request = %v, want FOFA request for %s", request, query)
		}
		field := request["fields"].(string)
		if field == "product3" && (request["size"] != 5 || request["uniq_count"] != nil) {
			t.Fatalf("service request changed: %v", request)
		}
		if field == "ip" && (request["size"] != nil || request["uniq_count"] != "ip") {
			t.Fatalf("invalid supplemental IP request: %v", request)
		}
		got = append(got, field)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("provider request fields = %v, want %v", got, want)
	}
}
