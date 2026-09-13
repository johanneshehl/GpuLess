package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed web/workflows/*.json
var builtinWorkflows embed.FS

// Workflow is a ComfyUI graph in API format plus the small amount of metadata
// the panel needs to fill it in and find its output.
type Workflow struct {
	Name    string
	Meta    WorkflowMeta
	Graph   map[string]json.RawMessage
	Builtin bool
}

// WorkflowMeta is the `_gpuless` block: it maps a parameter the panel knows
// about to the node input that carries it, so the graph itself stays a
// perfectly ordinary ComfyUI export the operator can replace.
type WorkflowMeta struct {
	Title       string              `json:"title"`
	Note        string              `json:"note"`
	OutputNodes []string            `json:"output_nodes"`
	Bind        map[string][]string `json:"bind"`
}

// Workflows holds the graphs in use, built-ins overridden by anything the
// operator dropped in the workflows directory.
type Workflows struct {
	dir  string
	list map[string]*Workflow
}

func LoadWorkflows(dir string) (*Workflows, error) {
	w := &Workflows{dir: dir, list: map[string]*Workflow{}}

	entries, err := builtinWorkflows.ReadDir("web/workflows")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		b, err := builtinWorkflows.ReadFile("web/workflows/" + e.Name())
		if err != nil {
			return nil, err
		}
		wf, err := parseWorkflow(b)
		if err != nil {
			return nil, fmt.Errorf("built-in %s: %w", e.Name(), err)
		}
		wf.Name = strings.TrimSuffix(e.Name(), ".json")
		wf.Builtin = true
		w.list[wf.Name] = wf
	}

	if dir == "" {
		return w, nil
	}
	overrides, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(overrides)
	for _, path := range overrides {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		wf, err := parseWorkflow(b)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		wf.Name = strings.TrimSuffix(filepath.Base(path), ".json")
		w.list[wf.Name] = wf
	}
	return w, nil
}

func parseWorkflow(b []byte) (*Workflow, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("not valid JSON: %w", err)
	}
	metaRaw, ok := raw["_gpuless"]
	if !ok {
		return nil, fmt.Errorf("missing the _gpuless block that says how to fill the graph in")
	}
	var meta WorkflowMeta
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		return nil, fmt.Errorf("_gpuless block: %w", err)
	}
	if len(meta.OutputNodes) == 0 {
		return nil, fmt.Errorf("_gpuless.output_nodes is empty, so the panel cannot find the result")
	}
	delete(raw, "_gpuless")
	if len(raw) == 0 {
		return nil, fmt.Errorf("the graph has no nodes")
	}
	for _, id := range meta.OutputNodes {
		if _, ok := raw[id]; !ok {
			return nil, fmt.Errorf("_gpuless.output_nodes names node %q, which is not in the graph", id)
		}
	}
	for name, target := range meta.Bind {
		if len(target) != 2 {
			return nil, fmt.Errorf("_gpuless.bind[%q] must be [node, input]", name)
		}
		if _, ok := raw[target[0]]; !ok {
			return nil, fmt.Errorf("_gpuless.bind[%q] points at node %q, which is not in the graph", name, target[0])
		}
	}
	return &Workflow{Meta: meta, Graph: raw}, nil
}

func (w *Workflows) Get(name string) (*Workflow, error) {
	wf, ok := w.list[name]
	if !ok {
		return nil, fmt.Errorf("no workflow called %q", name)
	}
	return wf, nil
}

func (w *Workflows) Names() []string {
	out := make([]string, 0, len(w.list))
	for n := range w.list {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Build returns a graph ready to post to ComfyUI, with `params` applied
// through the workflow's bindings. The template itself is never mutated.
func (wf *Workflow) Build(params map[string]any) (map[string]any, error) {
	graph := map[string]any{}
	for id, node := range wf.Graph {
		var decoded map[string]any
		if err := json.Unmarshal(node, &decoded); err != nil {
			return nil, fmt.Errorf("node %s: %w", id, err)
		}
		graph[id] = decoded
	}

	for name, value := range params {
		target, ok := wf.Meta.Bind[name]
		if !ok {
			return nil, fmt.Errorf("workflow %q has no binding for %q", wf.Name, name)
		}
		node, ok := graph[target[0]].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("node %s is not an object", target[0])
		}
		inputs, ok := node["inputs"].(map[string]any)
		if !ok {
			inputs = map[string]any{}
			node["inputs"] = inputs
		}
		inputs[target[1]] = value
	}
	return graph, nil
}
