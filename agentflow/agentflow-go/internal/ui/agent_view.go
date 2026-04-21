package ui

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"agentflow-go/internal/agent"
	"agentflow-go/internal/core"
	"agentflow-go/internal/model"
)

type agentView struct {
	app      *core.App
	win      fyne.Window
	output   *widget.RichText
	goalEntry *widget.Entry
	caseID   string // optional case ID to bind result to
}

func newAgentView(a *core.App, win fyne.Window) *agentView {
	return &agentView{app: a, win: win}
}

func (av *agentView) content() fyne.CanvasObject {
	// Case selector for binding the result
	cases := av.app.Workflow.ListCases()
	caseLabels := make([]string, 0, len(cases)+1)
	caseLabels = append(caseLabels, "(no case)")
	for _, c := range cases {
		caseLabels = append(caseLabels, fmt.Sprintf("%s — %s", c.ClientName, c.MatterType))
	}
	caseSelect := widget.NewSelect(caseLabels, func(sel string) {
		for i, c := range cases {
			if caseLabels[i+1] == sel {
				av.caseID = c.CaseID
				return
			}
		}
		av.caseID = ""
	})
	caseSelect.SetSelected("(no case)")

	// Goal input
	av.goalEntry = widget.NewMultiLineEntry()
	av.goalEntry.SetPlaceHolder("Describe your legal research goal in Chinese or English.\n\nExamples:\n• 分析本案证据，评估原告胜诉可能性\n• Extract all parties and amounts from the uploaded documents\n• Calculate the statute of limitations for this debt dispute")
	av.goalEntry.SetMinRowsVisible(5)

	// Output area
	av.output = widget.NewRichText()
	av.output.Wrapping = fyne.TextWrapWord

	runBtn := widget.NewButtonWithIcon("Run Agent", theme.MediaPlayIcon(), func() {
		goal := strings.TrimSpace(av.goalEntry.Text)
		if goal == "" {
			return
		}
		av.output.ParseMarkdown("*Running agent…*")
		jobID := av.app.AgentRun(goal, av.caseID)
		av.waitForResult(jobID)
	})

	// Quick-goal buttons
	quickGoals := []struct{ label, goal string }{
		{"Extract Entities", "Extract all parties, amounts, dates, and legal claims from the uploaded case documents."},
		{"Case Summary", "Generate a comprehensive case summary with evidence analysis and recommended next actions."},
		{"Deadline Check", "Identify the key legal deadlines for this case based on the matter type and relevant dates found in the documents."},
		{"Evidence Strength", "Analyze the strength of evidence for each document and identify any gaps in the case file."},
	}
	var quickBtns []fyne.CanvasObject
	for _, qg := range quickGoals {
		goal := qg.goal
		btn := widget.NewButton(qg.label, func() {
			av.goalEntry.SetText(goal)
		})
		quickBtns = append(quickBtns, btn)
	}
	quickRow := container.NewGridWithColumns(4, quickBtns...)

	header := container.NewVBox(
		widget.NewRichTextFromMarkdown("## AI Legal Agent"),
		widget.NewLabel("The agent reasons step-by-step and calls tools (RAG search, entity extraction, deadline calculator, etc.) to answer your goal."),
		widget.NewSeparator(),
		container.NewGridWithColumns(2,
			widget.NewLabel("Bind result to case:"),
			caseSelect,
		),
		quickRow,
		widget.NewSeparator(),
	)

	outputScroll := container.NewVScroll(av.output)

	return container.NewBorder(
		header,
		container.NewVBox(av.goalEntry, runBtn),
		nil, nil,
		outputScroll,
	)
}

func (av *agentView) waitForResult(jobID string) {
	// Poll the job store until the job completes, then display the result.
	// We subscribe to state changes so the UI updates automatically.
	av.app.Subscribe(func() {
		jobs := av.app.GetJobs()
		for _, j := range jobs {
			if j.ID != jobID {
				continue
			}
			switch j.Status {
			case model.JobStatusCompleted:
				if result, ok := j.Result.(agent.RunResult); ok {
					av.displayResult(result)
				} else {
					av.output.ParseMarkdown(fmt.Sprintf("**Done** — result: %v", j.Result))
				}
			case model.JobStatusFailed:
				av.output.ParseMarkdown(fmt.Sprintf("**Error**: %s", j.Error))
			}
			return
		}
	})
}

func (av *agentView) displayResult(r agent.RunResult) {
	var sb strings.Builder
	sb.WriteString("## Agent Result\n\n")
	sb.WriteString(r.Answer)

	if len(r.Steps) > 0 {
		sb.WriteString("\n\n---\n### Steps\n")
		for i, s := range r.Steps {
			if s.FinalAnswer != "" {
				continue
			}
			sb.WriteString(fmt.Sprintf("\n**Step %d** — `%s`\n", i+1, s.ToolName))
			if s.Output != nil {
				if s.Output.Error != "" {
					sb.WriteString(fmt.Sprintf("Error: %s\n", s.Output.Error))
				} else {
					preview := s.Output.Text
					if len(preview) > 300 {
						preview = preview[:300] + "…"
					}
					sb.WriteString(preview + "\n")
				}
			}
		}
	}

	if r.Truncated {
		sb.WriteString("\n\n*⚠ Agent reached step limit.*")
	}

	av.output.ParseMarkdown(sb.String())
}
