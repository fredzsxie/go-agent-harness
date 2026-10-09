package tool

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func echoHandler(_ context.Context, input any) (string, error) {
	return input.(map[string]any)["text"].(string), nil
}

func TestRegistryOwnsSchemaSnapshots(t *testing.T) {
	registry := NewRegistry()
	spec := Spec{Name: "echo", Required: []string{"text"}, Properties: map[string]any{
		"text": map[string]any{"type": "string", "enum": []any{"original"}},
	}}
	registry.Register(spec, echoHandler)
	spec.Required[0] = "changed"
	spec.Properties["text"].(map[string]any)["enum"].([]any)[0] = "changed"
	snapshot := registry.Specs()
	if snapshot[0].Required[0] != "text" || snapshot[0].Properties["text"].(map[string]any)["enum"].([]any)[0] != "original" {
		t.Fatal("Register retained caller-owned schema")
	}
	snapshot[0].Properties["text"].(map[string]any)["type"] = "number"
	if registry.Specs()[0].Properties["text"].(map[string]any)["type"] != "string" {
		t.Fatal("Specs leaked mutable schema")
	}
}

func TestRegistryAllowsRegistrationInsideHandler(t *testing.T) {
	registry := NewRegistry()
	registry.Register(Spec{Name: "connect"}, func(context.Context, any) (string, error) {
		registry.Register(Spec{Name: "discovered"}, echoHandler)
		return "connected", nil
	})
	done := make(chan error, 1)
	go func() { _, err := registry.Dispatch(context.Background(), "connect", nil); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("handler deadlocked while registering a discovered tool")
	}
	if got, err := registry.Dispatch(context.Background(), "discovered", map[string]any{"text": "ok"}); err != nil || got != "ok" {
		t.Fatalf("dispatch = %q, %v", got, err)
	}
}

func TestRegistryConcurrentDiscoveryAndDispatch(t *testing.T) {
	registry := NewRegistry()
	registry.Register(Spec{Name: "echo"}, echoHandler)
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				registry.Register(Spec{Name: fmt.Sprintf("dynamic_%d_%d", worker, i)}, echoHandler)
				if err := registry.Rebind("echo", echoHandler); err != nil {
					t.Error(err)
					return
				}
				selected, err := registry.Select("echo")
				if err != nil {
					t.Error(err)
					return
				}
				for _, current := range []*Registry{registry, selected} {
					if got, err := current.Dispatch(context.Background(), "echo", map[string]any{"text": "ok"}); err != nil || got != "ok" {
						t.Errorf("dispatch = %q, %v", got, err)
						return
					}
					_ = current.Specs()
				}
			}
		}(worker)
	}
	wg.Wait()
	if len(registry.Specs()) != 801 {
		t.Fatalf("lost registrations: %d", len(registry.Specs()))
	}
}
