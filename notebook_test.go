package main

import (
	"strings"
	"testing"
)

func TestRenderNotebookEmbedsSettings(t *testing.T) {
	src, err := RenderNotebook(NotebookParams{
		KernelToken: "secret-token",
		TunnelToken: "cf-token",
		IdleSeconds: 300,
		CapSeconds:  32400,
		Runtime:     "jhehl/gpuless-runtime",
		Accelerator: "T4x2",
		Models: []ModelMount{
			{Dataset: "jhehl/gpuless-sdxl", Folder: "checkpoints"},
			{Dataset: "jhehl/gpuless-xtts", Folder: "tts"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		`KERNEL_TOKEN = "secret-token"`,
		`TUNNEL_TOKEN = "cf-token"`,
		`IDLE_SECONDS = 300`,
		`CAP_SECONDS  = 32400`,
		`RUNTIME_DIR  = "/kaggle/input/gpuless-runtime"`,
		`"dir":"/kaggle/input/gpuless-sdxl"`,
		`"folder":"checkpoints"`,
		`"folder":"tts"`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("the runner is missing %s", want)
		}
	}
}

// The tunnel token and the shared secret are operator input that lands in
// Python source. A quote in either must not be able to end the literal.
func TestRenderNotebookEscapesInjection(t *testing.T) {
	nasty := `x"; import os; os.system("curl evil"); y = "`
	src, err := RenderNotebook(NotebookParams{
		KernelToken: nasty,
		TunnelToken: "a\\b\"c\nd",
		Runtime:     "o/rt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(src, `os.system("curl evil")`) {
		t.Fatal("a quote in the token escaped its literal — the runner is injectable")
	}
	if !strings.Contains(src, `KERNEL_TOKEN = "x\"; import os;`) {
		t.Errorf("the token should be escaped, not mangled; got:\n%s", firstLineWith(src, "KERNEL_TOKEN"))
	}
	if !strings.Contains(src, `TUNNEL_TOKEN = "a\\b\"c\nd"`) {
		t.Errorf("backslash and newline escaping is wrong: %s", firstLineWith(src, "TUNNEL_TOKEN"))
	}
}

func TestDatasetDir(t *testing.T) {
	cases := map[string]string{
		"jhehl/GPUless-Runtime": "/kaggle/input/gpuless-runtime",
		"owner/slug":            "/kaggle/input/slug",
		"slug":                  "/kaggle/input/slug",
	}
	for in, want := range cases {
		if got := datasetDir(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestRenderNotebookWithoutModels(t *testing.T) {
	// A runtime-only start is legitimate: the operator may still be uploading
	// weights. It must produce a script, not an error.
	src, err := RenderNotebook(NotebookParams{Runtime: "o/rt", KernelToken: "k", TunnelToken: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(src, "mounts = []") {
		t.Error("an empty mount list should render as an empty Python list")
	}
}

func TestModelsJSONShape(t *testing.T) {
	p := NotebookParams{Models: []ModelMount{{Dataset: "o/sdxl", Folder: "checkpoints", Subdir: "sd"}}}
	got := p.ModelsJSON()
	want := `[{"dir":"/kaggle/input/sdxl","folder":"checkpoints","subdir":"sd"}]`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func firstLineWith(s, needle string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	return ""
}
