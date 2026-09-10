# Terminal showcase

The four SVGs are derived from Bodek's actual `Model.View()` via
`TestTerminalWorkflowLayouts`, with synthetic checkout-review data. They are
rendered terminal fixtures, not screenshots of live provider execution.
Theme colors come from ANSI output; the SVG supplies a matching terminal
background. Browser monospace rendering can differ from a user's terminal font.

Refresh from the repository root:

```sh
BODEK_RENDER_PREVIEW_DIR=/tmp/bodek-landing-previews go test -race ./internal/tui -run '^TestTerminalWorkflowLayouts$' -count=1 -timeout=120s
python3 scripts/render-landing-gallery.py /tmp/bodek-landing-previews
```

The fixture controls the displayed model, version, status, and sample results.
