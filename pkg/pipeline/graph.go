package pipeline

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"text/template"

	"github.com/pkg/browser"
	"golang.org/x/term"
)

// graphHTMLTmpl is the HTML template for the interactive DAG visualization.
// stepKindMeta maps step kinds to their display label and hex color.
// Single source of truth — used by both graph nodes and the HTML legend.
var (
	graphHTMLTmpl = template.Must(template.New("graph").Parse(graphHTMLTemplate))

	stepKindMeta = map[StepKind]struct {
		Label string
		Color string
	}{
		StepPulumi:  {Label: "Pulumi", Color: "#4A90D9"},
		StepExec:    {Label: "Exec", Color: "#27AE60"},
		StepConfirm: {Label: "Confirm", Color: "#95A5A6"},
	}
)

// graphNode represents a node in the Cytoscape.js graph.
type (
	graphNode struct {
		ID    string
		Label string
		Color string
	}

	// graphEdge represents an edge in the Cytoscape.js graph.
	graphEdge struct {
		Source string
		Target string
	}

	// graphData holds all data needed to render the HTML template.
	graphData struct {
		Title  string
		Nodes  []graphNode
		Edges  []graphEdge
		Legend []graphLegendItem
	}

	// graphLegendItem represents a legend entry in the graph visualization.
	graphLegendItem struct {
		Label string
		Color string
	}
)

// stepKindColor returns the hex color for a step kind.
func stepKindColor(k StepKind) string {
	if meta, ok := stepKindMeta[k]; ok {
		return meta.Color
	}

	return "#BDC3C7"
}

// GenerateGraphHTML generates an interactive HTML DAG visualization of the pipeline steps.
func GenerateGraphHTML(steps []Step) []byte {
	var nodes []graphNode

	for i := range steps {
		nodes = append(nodes, graphNode{
			ID:    steps[i].Name,
			Label: steps[i].Name,
			Color: stepKindColor(steps[i].Kind),
		})
	}

	var edges []graphEdge

	for i := range steps {
		for _, dep := range steps[i].DependsOn {
			edges = append(edges, graphEdge{
				Source: dep,
				Target: steps[i].Name,
			})
		}
	}

	// Build legend items from the single source of truth.
	legendOrder := []StepKind{StepPulumi, StepExec, StepConfirm}
	legendItems := make([]graphLegendItem, 0, len(legendOrder))

	for _, k := range legendOrder {
		if meta, ok := stepKindMeta[k]; ok {
			legendItems = append(legendItems, graphLegendItem{Label: meta.Label, Color: meta.Color})
		}
	}

	data := graphData{
		Title:  "Pipeline DAG",
		Nodes:  nodes,
		Edges:  edges,
		Legend: legendItems,
	}

	var buf bytes.Buffer
	if err := graphHTMLTmpl.Execute(&buf, data); err != nil {
		// Template is compiled at init time; execution errors are programming bugs.
		panic(fmt.Sprintf("execute graph template: %v", err))
	}

	return buf.Bytes()
}

// GraphCommand generates the HTML graph and handles output/browser opening.
func GraphCommand(cfg Config, outputPath string) error {
	sorted, err := TopologicalSort(cfg.Steps)
	if err != nil {
		return fmt.Errorf("topological sort: %w", err)
	}

	html := GenerateGraphHTML(sorted)

	if outputPath != "" {
		if writeErr := os.WriteFile(outputPath, html, 0o644); writeErr != nil {
			return fmt.Errorf("write graph HTML: %w", writeErr)
		}

		_, _ = fmt.Fprintln(os.Stdout, outputPath)

		return nil
	}

	tmpFile, err := os.CreateTemp("", "pulumi-pipeline-graph-*.html")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	if _, writeErr := tmpFile.Write(html); writeErr != nil {
		_ = tmpFile.Close()

		return fmt.Errorf("write temp file: %w", writeErr)
	}

	if closeErr := tmpFile.Close(); closeErr != nil {
		return fmt.Errorf("close temp file: %w", closeErr)
	}

	path := tmpFile.Name()

	if term.IsTerminal(int(os.Stdout.Fd())) {
		if openErr := browser.OpenFile(path); openErr != nil {
			log.Printf("warning: could not open browser: %v", openErr)
		}
	}

	_, _ = fmt.Fprintln(os.Stdout, path)

	return nil
}

const (
	graphHTMLTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.Title}}</title>
<style>
  body { margin: 0; padding: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
  #cy { width: 100vw; height: 100vh; }
  #legend {
    position: absolute; top: 12px; right: 12px;
    background: rgba(255,255,255,0.95); border: 1px solid #ddd; border-radius: 6px;
    padding: 10px 14px; font-size: 13px; line-height: 1.8; z-index: 10;
  }
  #legend h3 { margin: 0 0 6px; font-size: 14px; }
  .legend-item { display: flex; align-items: center; gap: 8px; }
  .legend-dot { width: 12px; height: 12px; border-radius: 50%; display: inline-block; }
</style>
</head>
<body>
<div id="cy"></div>
<div id="legend">
  <h3>{{.Title}}</h3>
  {{- range .Legend}}
  <div class="legend-item"><span class="legend-dot" style="background:{{.Color}}"></span> {{.Label}}</div>
  {{- end}}
</div>
<script src="https://unpkg.com/cytoscape@3/dist/cytoscape.min.js"></script>
<script src="https://unpkg.com/dagre@0.8/dist/dagre.min.js"></script>
<script src="https://unpkg.com/cytoscape-dagre@2/cytoscape-dagre.js"></script>
<script>
cytoscape.use(cytoscapeDagre);
var cy = cytoscape({
  container: document.getElementById('cy'),
  elements: [
    {{- range .Nodes}}
    { data: { id: '{{.ID}}', label: '{{.Label}}' }, style: { 'background-color': '{{.Color}}' } },
    {{- end}}
    {{- range .Edges}}
    { data: { source: '{{.Source}}', target: '{{.Target}}' } },
    {{- end}}
  ],
  style: [
    { selector: 'node', style: {
      'label': 'data(label)', 'text-valign': 'center', 'text-halign': 'center',
      'color': '#fff', 'font-size': '11px', 'font-weight': 'bold',
      'width': 'label', 'height': 32, 'padding': '12px',
      'shape': 'round-rectangle', 'text-wrap': 'wrap'
    }},
    { selector: 'edge', style: {
      'width': 2, 'line-color': '#aaa', 'target-arrow-color': '#aaa',
      'target-arrow-shape': 'triangle', 'curve-style': 'bezier'
    }}
  ],
  layout: { name: 'dagre', rankDir: 'TB', nodeSep: 40, rankSep: 60 }
});
</script>
</body>
</html>`
)
