package gallery

import "testing"

func TestModuleRegistered(t *testing.T) {
	// Smoke test: the init() above registers the module. We can verify
	// the type is constructable and the ModuleInfo is correct.
	var g Gallery
	info := g.CaddyModule()
	if info.ID != "http.handlers.image_gallery" {
		t.Errorf("expected module ID %q, got %q", "http.handlers.image_gallery", info.ID)
	}
	if info.New == nil {
		t.Error("expected New constructor to be non-nil")
	}
}
