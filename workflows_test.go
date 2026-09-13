package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltinWorkflowsLoad(t *testing.T) {
	w, err := LoadWorkflows("")
	if err != nil {
		t.Fatalf("built-in workflows must always parse: %v", err)
	}
	for _, name := range []string{"image", "voice"} {
		wf, err := w.Get(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !wf.Builtin {
			t.Errorf("%s should be marked built-in", name)
		}
		if len(wf.Meta.OutputNodes) == 0 {
			t.Errorf("%s has no output nodes", name)
		}
	}
	if _, err := w.Get("nope"); err == nil {
		t.Error("an unknown workflow should be an error")
	}
}

func TestWorkflowBuildAppliesBindings(t *testing.T) {
	w, _ := LoadWorkflows("")
	wf, _ := w.Get("image")

	graph, err := wf.Build(map[string]any{
		"prompt": "a lighthouse", "negative": "blurry",
		"width": 768, "height": 768, "steps": 30, "guidance": 7.5, "seed": int64(1234),
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := inputOf(t, graph, "6", "text"); got != "a lighthouse" {
		t.Errorf("prompt landed as %v", got)
	}
	if got := inputOf(t, graph, "7", "text"); got != "blurry" {
		t.Errorf("negative landed as %v", got)
	}
	if got := inputOf(t, graph, "5", "width"); got != 768 {
		t.Errorf("width landed as %v", got)
	}
	if got := inputOf(t, graph, "3", "steps"); got != 30 {
		t.Errorf("steps landed as %v", got)
	}
	// The links between nodes must survive untouched, or ComfyUI rejects it.
	node := graph["3"].(map[string]any)["inputs"].(map[string]any)
	if _, ok := node["model"].([]any); !ok {
		t.Error("the model link was lost while filling the graph in")
	}
}

func TestWorkflowBuildDoesNotMutateTheTemplate(t *testing.T) {
	w, _ := LoadWorkflows("")
	wf, _ := w.Get("image")

	if _, err := wf.Build(map[string]any{"prompt": "first"}); err != nil {
		t.Fatal(err)
	}
	graph, err := wf.Build(map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if got := inputOf(t, graph, "6", "text"); got != "" {
		t.Errorf("the template kept %q from the previous build", got)
	}
}

func TestWorkflowBuildRejectsUnknownParameter(t *testing.T) {
	w, _ := LoadWorkflows("")
	wf, _ := w.Get("image")
	if _, err := wf.Build(map[string]any{"nonsense": 1}); err == nil {
		t.Error("an unbound parameter should be refused rather than silently dropped")
	}
}

func TestWorkflowOverrideWins(t *testing.T) {
	dir := t.TempDir()
	custom := `{
	  "_gpuless": {"output_nodes": ["out"], "bind": {"prompt": ["a", "text"]}},
	  "a": {"class_type": "Mine", "inputs": {"text": ""}},
	  "out": {"class_type": "SaveImage", "inputs": {"images": ["a", 0]}}
	}`
	if err := os.WriteFile(filepath.Join(dir, "image.json"), []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}

	w, err := LoadWorkflows(dir)
	if err != nil {
		t.Fatal(err)
	}
	wf, _ := w.Get("image")
	if wf.Builtin {
		t.Fatal("the override should replace the built-in")
	}
	if wf.Meta.OutputNodes[0] != "out" {
		t.Errorf("override metadata not used: %+v", wf.Meta)
	}
	// The built-in that was not overridden must still be there.
	if _, err := w.Get("voice"); err != nil {
		t.Errorf("overriding one workflow dropped the others: %v", err)
	}
}

func TestWorkflowValidation(t *testing.T) {
	bad := map[string]string{
		"not json":            `{`,
		"no metadata":         `{"1": {"class_type": "X"}}`,
		"no output nodes":     `{"_gpuless": {"output_nodes": []}, "1": {"class_type": "X"}}`,
		"output node missing": `{"_gpuless": {"output_nodes": ["9"]}, "1": {"class_type": "X"}}`,
		"binding to nowhere":  `{"_gpuless": {"output_nodes": ["1"], "bind": {"p": ["9", "text"]}}, "1": {"class_type": "X"}}`,
		"malformed binding":   `{"_gpuless": {"output_nodes": ["1"], "bind": {"p": ["1"]}}, "1": {"class_type": "X"}}`,
		"no nodes":            `{"_gpuless": {"output_nodes": ["1"]}}`,
	}
	for name, src := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := parseWorkflow([]byte(src)); err == nil {
				t.Error("should have been rejected")
			}
		})
	}
}

func TestLoadWorkflowsReportsTheBadFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "image.json"), []byte(`{"nope": true}`), 0o600)

	_, err := LoadWorkflows(dir)
	if err == nil {
		t.Fatal("a broken override must fail loudly at start, not at first use")
	}
	if !strings.Contains(err.Error(), "image.json") {
		t.Errorf("the error should name the file, got: %v", err)
	}
}

func inputOf(t *testing.T, graph map[string]any, node, input string) any {
	t.Helper()
	n, ok := graph[node].(map[string]any)
	if !ok {
		t.Fatalf("node %s missing", node)
	}
	inputs, ok := n["inputs"].(map[string]any)
	if !ok {
		t.Fatalf("node %s has no inputs", node)
	}
	v := inputs[input]
	// Numbers survive a JSON round trip as float64; compare as int where the
	// caller asked for one.
	if f, ok := v.(float64); ok && f == float64(int(f)) {
		return int(f)
	}
	return v
}

func TestWorkflowJSONStaysValid(t *testing.T) {
	// A graph that does not marshal is one ComfyUI will never see.
	w, _ := LoadWorkflows("")
	for _, name := range w.Names() {
		wf, _ := w.Get(name)
		graph, err := wf.Build(nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := json.Marshal(graph); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
