package server

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"agentflow-go/internal/model"
)

// writeDocx writes a minimal-but-valid OpenXML .docx archive to w. Used by
// both the legacy single-draft export (`handleDraftExportDocx`) and the new
// typed-document export (`handleGeneratedDocExport`).
//
// We deliberately hand-build the OpenXML rather than pulling in unioffice or
// docx-go: the required surface (title + section heading + paragraphs +
// red-coloured highlight footnotes) fits in ~50 lines, and avoiding the
// dependency keeps the binary small and the build hermetic.
//
// The output renders cleanly in Word, Pages, and LibreOffice.
func writeDocx(w io.Writer, title string, sections []model.DocSection) error {
	var bodyXML strings.Builder
	bodyXML.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">`)
	bodyXML.WriteString(`<w:body>`)

	esc := func(s string) string {
		var b strings.Builder
		_ = xml.EscapeText(&b, []byte(s))
		return b.String()
	}

	// Title — bold, large.
	if title != "" {
		bodyXML.WriteString(fmt.Sprintf(
			`<w:p><w:pPr><w:jc w:val="center"/></w:pPr><w:r><w:rPr><w:b/><w:sz w:val="36"/></w:rPr><w:t xml:space="preserve">%s</w:t></w:r></w:p>`,
			esc(title),
		))
	}

	for _, sec := range sections {
		// Section heading — bold.
		if sec.Title != "" {
			bodyXML.WriteString(fmt.Sprintf(
				`<w:p><w:r><w:rPr><w:b/><w:sz w:val="28"/></w:rPr><w:t xml:space="preserve">%s</w:t></w:r></w:p>`,
				esc(sec.Title),
			))
		}
		// Body paragraphs — split on blank lines.
		for _, para := range strings.Split(sec.Content, "\n\n") {
			para = strings.TrimSpace(para)
			if para == "" {
				continue
			}
			// Single-newline → soft break inside the same paragraph.
			lines := strings.Split(para, "\n")
			var paraBuf strings.Builder
			paraBuf.WriteString(`<w:p>`)
			for i, line := range lines {
				if i > 0 {
					paraBuf.WriteString(`<w:r><w:br/></w:r>`)
				}
				paraBuf.WriteString(fmt.Sprintf(
					`<w:r><w:t xml:space="preserve">%s</w:t></w:r>`,
					esc(line),
				))
			}
			paraBuf.WriteString(`</w:p>`)
			bodyXML.WriteString(paraBuf.String())
		}
		// Highlight footer — every cited claim rendered in muted red so the
		// lawyer can scan source attributions while reviewing.
		for _, h := range sec.Highlights {
			line := fmt.Sprintf("【核实】%s | 原因: %s | 来源: %s%s",
				h.Text, h.Reason, h.SourceFile,
				ifNonEmpty(h.SourceRef, " §"+h.SourceRef))
			bodyXML.WriteString(fmt.Sprintf(
				`<w:p><w:r><w:rPr><w:color w:val="C0504D"/><w:sz w:val="18"/></w:rPr><w:t xml:space="preserve">%s</w:t></w:r></w:p>`,
				esc(line),
			))
		}
	}

	bodyXML.WriteString(`</w:body></w:document>`)

	zw := zip.NewWriter(w)
	add := func(name, content string) error {
		f, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = f.Write([]byte(content))
		return err
	}
	if err := add("[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`); err != nil {
		return err
	}
	if err := add("_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`); err != nil {
		return err
	}
	if err := add("word/_rels/document.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"></Relationships>`); err != nil {
		return err
	}
	if err := add("word/document.xml",
		`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+bodyXML.String()); err != nil {
		return err
	}
	return zw.Close()
}

func ifNonEmpty(s, t string) string {
	if s == "" {
		return ""
	}
	return t
}
