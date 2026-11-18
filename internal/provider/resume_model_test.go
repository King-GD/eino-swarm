package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	swarm "github.com/LubyRuffy/eino-swarm"
	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func TestRecoveredGatewayCannotBorrowPresetOrRetainRevokedAuthority(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "generate", true: "stream"}[streaming], func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				if r.Header.Get("Authorization") != "Bearer original-token" {
					t.Error("recovery changed the credential")
				}
				if streaming {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, backoffChunk+"data: [DONE]\n\n")
				} else {
					_, _ = io.WriteString(w, backoffCompletion)
				}
			}))
			defer srv.Close()
			pool := New(configFor(t, true))
			source := accountSourceFor(t, srv.URL, "original-token")
			source.SetCatalogGroup("original")
			source.SetCapabilities(roleManifest("conversation", "chat_completions", true))
			preset := accountSourceFor(t, "https://preset.invalid/v1", "preset-token")
			preset.id = "gateway:preset"
			preset.SetModelPolicy(false, map[string]string{"conversation": "server-model"})
			pool.SetAccountSource(preset)
			pool.SetLocalAccountSources([]*AccountSource{source})
			build, err := pool.RestoreWorkerModel(t.Context(), swarm.ModelBinding{ProviderID: source.id, Model: "server-model"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			client := build("worker", "restored")
			if streaming {
				stream, err := client.Stream(t.Context(), backoffMessages())
				if err != nil {
					t.Fatal(err)
				}
				if err := finishBackoffStream(stream); err != nil {
					t.Fatal(err)
				}
				stream.Close()
			} else if _, err := client.Generate(t.Context(), backoffMessages()); err != nil {
				t.Fatal(err)
			}
			pool.SetLocalAccountSources(nil)
			if _, err := client.Generate(t.Context(), backoffMessages()); !errors.Is(err, ErrModelPolicy) || hits.Load() != 1 {
				t.Fatalf("revoked source reached HTTP: hits=%d err=%v", hits.Load(), err)
			}
		})
	}
}

func TestResumeModelValidatesOnlyAuthorizedDurableBindings(t *testing.T) {
	pool := New(configFor(t, true))
	source := accountSourceFor(t, "https://gateway.invalid/v1", "token")
	if _, err := pool.ResumeModelBuilder(t.Context(), source.id, "server-model", "", nil); !errors.Is(err, ErrAccountLoginRequired) {
		t.Fatal(err)
	}
	pool.SetAccountSource(source)
	source.SetModelPolicy(false, map[string]string{"conversation": "server-model"})
	if _, err := pool.RestoreWorkerModel(t.Context(), swarm.ModelBinding{ProviderID: pool.cfg.ModelsSnapshot().Default, Model: "personal-model"}, nil); !errors.Is(err, ErrModelPolicy) {
		t.Fatalf("recovery admitted an unauthorized personal source: %v", err)
	}
	if _, err := pool.ResumeModelBuilder(t.Context(), source.id, "removed-model", "", nil); !errors.Is(err, ErrModelPolicy) {
		t.Fatal(err)
	}
	source.SetCapabilities(nil)
	if _, err := pool.ResumeModelBuilder(t.Context(), source.id, "server-model", "", nil); !errors.Is(err, ErrAccountModelsUnavailable) {
		t.Fatal(err)
	}
	source.SetCapabilities(roleManifest("conversation", "chat_completions", true))
	source.unavailable = true
	if _, err := pool.ResumeModelBuilder(t.Context(), source.id, "server-model", "", nil); !errors.Is(err, ErrAccountModelsUnavailable) {
		t.Fatal(err)
	}
	source.unavailable = false
	refused := errors.New("build refused")
	pool.build = func(context.Context, config.Provider) (model.BaseChatModel, error) { return nil, refused }
	if _, err := pool.ResumeModelBuilder(t.Context(), source.id, "", "", nil); !errors.Is(err, refused) {
		t.Fatal(err)
	}
	pool.build = func(context.Context, config.Provider) (model.BaseChatModel, error) { return &fakeModel{}, nil }
	build, err := pool.ResumeModelBuilder(t.Context(), source.id, "", "high", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := build("manager", "manager").Generate(t.Context(), []*schema.Message{schema.UserMessage("resume")}); err != nil {
		t.Fatal(err)
	}
	source.SetModelPolicy(true, nil)
	if _, err := pool.ResumeModelBuilder(t.Context(), "missing", "model", "", nil); !errors.Is(err, ErrUnknownProvider) {
		t.Fatal(err)
	}
	mock := NewMock(configFor(t, true))
	if _, err := mock.ResumeModelBuilder(t.Context(), "", "", "", nil); err != nil {
		t.Fatal(err)
	}
}

func TestAccountRecoveryWaitsForOwnedDiscoveryAndPreservesCancellation(t *testing.T) {
	for _, failure := range []error{nil, context.Canceled} {
		pool := New(configFor(t, true))
		source := accountSourceFor(t, "https://gateway.invalid/v1", "token")
		pool.SetAccountSource(NewUnavailableAccountSource("gateway:pending", "Pending", "https://gateway.invalid/v1", "token", false))
		pool.build = func(context.Context, config.Provider) (model.BaseChatModel, error) { return &fakeModel{}, nil }
		refreshed := false
		pool.SetCapabilitiesRefresher(func(ctx context.Context) error {
			refreshed = true
			if failure != nil {
				<-ctx.Done()
				return ctx.Err()
			}
			pool.SetAccountSource(source)
			return nil
		})
		ctx, cancel := context.WithCancel(t.Context())
		if failure != nil {
			cancel()
		}
		builder, err := pool.ResumeModelBuilder(ctx, source.id, "server-model", "", nil)
		cancel()
		if !refreshed || !errors.Is(err, failure) || failure == nil && builder == nil {
			t.Fatalf("owned discovery: refreshed=%v builder=%v err=%v", refreshed, builder != nil, err)
		}
	}
}
