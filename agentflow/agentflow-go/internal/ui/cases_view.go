package ui

import (
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"agentflow-go/internal/core"
	"agentflow-go/internal/model"
)

type casesView struct {
	app    *core.App
	win    fyne.Window
	list   *widget.List
	detail *fyne.Container
	cases  []model.Case

	clientEntry  *widget.Entry
	matterSelect *widget.Select
	addBtn       *widget.Button
}

func newCasesView(a *core.App, win fyne.Window) *casesView {
	cv := &casesView{app: a, win: win}
	cv.cases = a.Workflow.ListCases()
	return cv
}

func (cv *casesView) content() fyne.CanvasObject {
	cv.list = widget.NewList(
		func() int { return len(cv.cases) },
		func() fyne.CanvasObject {
			return container.NewBorder(nil, nil, nil, nil,
				widget.NewLabel("case"),
			)
		},
		func(id widget.ListItemID, item fyne.CanvasObject) {
			if id >= len(cv.cases) {
				return
			}
			c := cv.cases[id]
			lbl := item.(*fyne.Container).Objects[0].(*widget.Label)
			lbl.SetText(fmt.Sprintf("%s — %s [%s]", c.ClientName, c.MatterType, c.State))
		},
	)
	cv.list.OnSelected = func(id widget.ListItemID) {
		if id < len(cv.cases) {
			cv.showDetail(cv.cases[id])
		}
	}

	cv.detail = container.NewStack(widget.NewLabel("Select a case to view details"))

	// New case form
	cv.clientEntry = widget.NewEntry()
	cv.clientEntry.SetPlaceHolder("Client name")

	matterTypes := []string{
		"Civil Litigation", "Contract Dispute", "Sales Contract Dispute",
		"Debt Dispute", "Loan Dispute", "Lease Dispute", "Labor Dispute",
		"Commercial Lease Dispute",
	}
	cv.matterSelect = widget.NewSelect(matterTypes, nil)
	cv.matterSelect.SetSelected("Civil Litigation")

	cv.addBtn = widget.NewButtonWithIcon("New Case", theme.ContentAddIcon(), func() {
		name := strings.TrimSpace(cv.clientEntry.Text)
		if name == "" {
			dialog.ShowError(fmt.Errorf("client name is required"), cv.win)
			return
		}
		cv.app.Workflow.CreateCase(name, cv.matterSelect.Selected, "Manual", "")
		cv.clientEntry.SetText("")
		cv.refresh()
	})

	newCaseForm := container.NewVBox(
		widget.NewSeparator(),
		widget.NewLabel("New Case"),
		cv.clientEntry,
		cv.matterSelect,
		cv.addBtn,
	)

	// Upload button
	uploadBtn := widget.NewButtonWithIcon("Upload Document", theme.UploadIcon(), func() {
		cv.showUploadDialog()
	})

	leftPanel := container.NewBorder(
		container.NewVBox(uploadBtn, widget.NewSeparator()),
		newCaseForm,
		nil, nil,
		cv.list,
	)

	split := container.NewHSplit(leftPanel, cv.detail)
	split.SetOffset(0.35)
	return split
}

func (cv *casesView) showDetail(c model.Case) {
	// Header
	header := widget.NewRichTextFromMarkdown(
		fmt.Sprintf("## %s\n**%s** · %s", c.ClientName, c.MatterType, c.State),
	)

	// Documents
	docItems := make([]string, len(c.UploadedDocuments))
	for i, d := range c.UploadedDocuments {
		docItems[i] = "• " + d
	}
	docsText := "No documents"
	if len(docItems) > 0 {
		docsText = strings.Join(docItems, "\n")
	}
	docsLabel := widget.NewLabel(docsText)
	docsLabel.Wrapping = fyne.TextWrapWord

	// Notes
	notesItems := make([]string, len(c.Notes))
	for i, n := range c.Notes {
		notesItems[i] = fmt.Sprintf("[%s] %s", n.Timestamp.Format("Jan 2 15:04"), n.Text)
	}
	notesText := "No notes"
	if len(notesItems) > 0 {
		notesText = strings.Join(notesItems, "\n")
	}
	notesLabel := widget.NewLabel(notesText)
	notesLabel.Wrapping = fyne.TextWrapWord

	// AI summary
	summaryText := c.AICaseSummary
	if summaryText == "" {
		summaryText = "(no AI summary yet)"
	}
	summaryLabel := widget.NewLabel(summaryText)
	summaryLabel.Wrapping = fyne.TextWrapWord

	// Add note
	noteEntry := widget.NewMultiLineEntry()
	noteEntry.SetPlaceHolder("Add a note…")
	noteEntry.SetMinRowsVisible(2)
	addNoteBtn := widget.NewButton("Add Note", func() {
		text := strings.TrimSpace(noteEntry.Text)
		if text == "" {
			return
		}
		cv.app.Workflow.AddNote(c.CaseID, text)
		noteEntry.SetText("")
		cv.refresh()
	})

	// Advance state
	advanceBtn := widget.NewButtonWithIcon("Advance State", theme.NavigateNextIcon(), func() {
		if err := cv.app.Workflow.AdvanceState(c.CaseID); err != nil {
			dialog.ShowError(err, cv.win)
		} else {
			cv.refresh()
		}
	})

	// Delete case
	deleteBtn := widget.NewButtonWithIcon("Delete Case", theme.DeleteIcon(), func() {
		dialog.ShowConfirm("Delete Case",
			fmt.Sprintf("Delete case for %s?", c.ClientName),
			func(ok bool) {
				if !ok {
					return
				}
				if err := cv.app.Workflow.DeleteCase(c.CaseID); err != nil {
					dialog.ShowError(err, cv.win)
				} else {
					cv.detail.Objects = []fyne.CanvasObject{widget.NewLabel("Select a case")}
					cv.detail.Refresh()
					cv.refresh()
				}
			}, cv.win)
	})

	createdAt := c.CreatedAt.Format(time.RFC3339)
	metaLabel := widget.NewLabel(fmt.Sprintf("ID: %s\nCreated: %s", c.CaseID, createdAt))
	metaLabel.TextStyle = fyne.TextStyle{Monospace: true}

	detailContent := container.NewVScroll(container.NewVBox(
		header,
		widget.NewSeparator(),
		widget.NewLabel("Documents"),
		docsLabel,
		widget.NewSeparator(),
		widget.NewLabel("AI Summary"),
		summaryLabel,
		widget.NewSeparator(),
		widget.NewLabel("Notes"),
		notesLabel,
		noteEntry,
		addNoteBtn,
		widget.NewSeparator(),
		container.NewGridWithColumns(2, advanceBtn, deleteBtn),
		widget.NewSeparator(),
		metaLabel,
	))

	cv.detail.Objects = []fyne.CanvasObject{detailContent}
	cv.detail.Refresh()
}

func (cv *casesView) showUploadDialog() {
	// Select a case (optional)
	cases := cv.app.Workflow.ListCases()
	caseLabels := make([]string, 0, len(cases)+1)
	caseLabels = append(caseLabels, "(auto-assign)")
	for _, c := range cases {
		caseLabels = append(caseLabels, fmt.Sprintf("%s — %s", c.ClientName, c.MatterType))
	}
	caseSelect := widget.NewSelect(caseLabels, nil)
	caseSelect.SetSelected("(auto-assign)")

	dialog.ShowFileOpen(func(f fyne.URIReadCloser, err error) {
		if err != nil || f == nil {
			return
		}
		defer f.Close()

		filePath := f.URI().Path()
		var reqCaseID string
		sel := caseSelect.Selected
		if sel != "(auto-assign)" {
			for i, c := range cases {
				if caseLabels[i+1] == sel {
					reqCaseID = c.CaseID
					break
				}
			}
		}

		jobID, err := cv.app.UploadFile(filePath, reqCaseID)
		if err != nil {
			dialog.ShowError(err, cv.win)
			return
		}
		dialog.ShowInformation("Upload Queued",
			fmt.Sprintf("File queued for processing.\nJob ID: %s", jobID),
			cv.win)
	}, cv.win)
}

func (cv *casesView) refresh() {
	cv.cases = cv.app.Workflow.ListCases()
	if cv.list != nil {
		cv.list.Refresh()
	}
}
