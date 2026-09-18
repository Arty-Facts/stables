package ollama

import (
	"strings"
	"testing"

	"github.com/stables/stables/internal/assets"
)

// skipWithoutAssets skips when the generated runtime assets are not staged,
// which is the case on a fresh clone that has not run `make assets-common`.
func skipWithoutAssets(t *testing.T) {
	t.Helper()
	if !assets.Has("ollama/docker-compose.yml.tmpl") {
		t.Skip("runtime assets not staged: run `make assets-common`")
	}
}

func TestRenderComposeDefaultsExposeLANAndUseHomeData(t *testing.T) {
	skipWithoutAssets(t)
	d := DefaultData("/home/alice")
	d.GPUEnabled = true
	b, err := RenderCompose(d)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		`"0.0.0.0:11434:11434"`,
		`/home/alice/.stables/ollama/data:/root/.ollama`,
		`OLLAMA_CONTEXT_LENGTH=131072`,
		`OLLAMA_FLASH_ATTENTION=1`,
		`gpus: all`,
		`restart: always`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("compose missing %q:\n%s", want, s)
		}
	}
	// WebUI is a separate component and must not leak into the ollama stack.
	if strings.Contains(s, "open-webui") || strings.Contains(s, "3000:8080") {
		t.Fatalf("ollama stack must not contain open-webui:\n%s", s)
	}
	// Empty subnet must not render an ipam block (compose rejects empty subnet).
	if strings.Contains(s, "ipam:") {
		t.Fatalf("empty subnet must not emit ipam:\n%s", s)
	}
}

func TestRenderComposeExplicitSubnet(t *testing.T) {
	skipWithoutAssets(t)
	d := DefaultData("/home/alice")
	d.NetworkSubnet = "10.250.1.0/24"
	b, err := RenderCompose(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "10.250.1.0/24") {
		t.Fatalf("explicit subnet missing:\n%s", b)
	}
}
