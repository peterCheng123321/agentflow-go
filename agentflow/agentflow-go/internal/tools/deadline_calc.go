package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agentflow-go/internal/agent"
)

// DeadlineCalc computes key legal deadlines based on jurisdiction and matter type.
// All rules are encoded for Chinese civil litigation (PRC Civil Procedure Law).
type DeadlineCalc struct{}

func NewDeadlineCalc() *DeadlineCalc { return &DeadlineCalc{} }

func (t *DeadlineCalc) Name() string { return "calculate_deadlines" }
func (t *DeadlineCalc) Description() string {
	return "Calculate key legal deadlines: statute of limitations, response deadlines, appeal windows, and filing cutoffs based on the case facts and PRC Civil Procedure Law."
}
func (t *DeadlineCalc) Params() map[string]agent.ParamSchema {
	return map[string]agent.ParamSchema{
		"matter_type":      {Description: "One of: Civil Litigation, Contract Dispute, Debt Dispute, Loan Dispute, Lease Dispute, Labor Dispute", Required: true},
		"event_date":       {Description: "Date the cause of action arose (YYYY-MM-DD)", Required: true},
		"reference_date":   {Description: "Reference date for calculating remaining time (YYYY-MM-DD, defaults to today)"},
		"judgment_date":    {Description: "Date of first-instance judgment, if applicable (YYYY-MM-DD)"},
	}
}

type DeadlineResult struct {
	StatuteOfLimitations deadline `json:"statute_of_limitations"`
	AppealWindow         *deadline `json:"appeal_window,omitempty"`
	ResponseDeadline     *deadline `json:"response_deadline,omitempty"`
	Warnings             []string  `json:"warnings"`
}

type deadline struct {
	Label      string    `json:"label"`
	Cutoff     time.Time `json:"cutoff"`
	DaysLeft   int       `json:"days_left"`
	Expired    bool      `json:"expired"`
	LegalBasis string    `json:"legal_basis"`
}

func (t *DeadlineCalc) Execute(_ context.Context, input agent.ToolInput) agent.ToolOutput {
	matterType, _ := input["matter_type"].(string)
	eventDateStr, _ := input["event_date"].(string)

	if matterType == "" || eventDateStr == "" {
		return agent.ToolOutput{Error: "matter_type and event_date are required"}
	}

	eventDate, err := time.Parse("2006-01-02", eventDateStr)
	if err != nil {
		return agent.ToolOutput{Error: fmt.Sprintf("invalid event_date %q: use YYYY-MM-DD", eventDateStr)}
	}

	refDate := time.Now()
	if s, _ := input["reference_date"].(string); s != "" {
		if d, err := time.Parse("2006-01-02", s); err == nil {
			refDate = d
		}
	}

	result := DeadlineResult{}

	// PRC statute of limitations by matter type
	// 民法典 第188条: general 3-year limitation
	// 劳动争议调解仲裁法 第27条: 1-year for labor
	solYears := 3
	solBasis := "《民法典》第188条（一般诉讼时效3年）"
	switch {
	case strings.Contains(matterType, "Labor"):
		solYears = 1
		solBasis = "《劳动争议调解仲裁法》第27条（劳动争议时效1年）"
	case strings.Contains(matterType, "Lease"):
		// Special rule: rent disputes 1 year under old law, 3 under new
		solBasis = "《民法典》第188条（租赁合同纠纷时效3年）"
	}

	solCutoff := eventDate.AddDate(solYears, 0, 0)
	daysLeft := int(solCutoff.Sub(refDate).Hours() / 24)
	result.StatuteOfLimitations = deadline{
		Label:      fmt.Sprintf("诉讼时效 (%d年)", solYears),
		Cutoff:     solCutoff,
		DaysLeft:   daysLeft,
		Expired:    daysLeft < 0,
		LegalBasis: solBasis,
	}

	// Appeal window: 15 days from judgment (civil, 《民事诉讼法》第164条)
	if s, _ := input["judgment_date"].(string); s != "" {
		jd, err := time.Parse("2006-01-02", s)
		if err == nil {
			appealCutoff := jd.AddDate(0, 0, 15)
			adl := int(appealCutoff.Sub(refDate).Hours() / 24)
			result.AppealWindow = &deadline{
				Label:      "上诉期限 (15天)",
				Cutoff:     appealCutoff,
				DaysLeft:   adl,
				Expired:    adl < 0,
				LegalBasis: "《民事诉讼法》第164条（上诉期15天）",
			}
		}
	}

	// Warnings
	if result.StatuteOfLimitations.Expired {
		result.Warnings = append(result.Warnings, "⚠️  诉讼时效已过期，需主张中断或特殊情形（如对方认可债务）")
	} else if result.StatuteOfLimitations.DaysLeft < 30 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("⚠️  诉讼时效仅剩 %d 天，建议立即立案", result.StatuteOfLimitations.DaysLeft))
	}
	if result.AppealWindow != nil && result.AppealWindow.Expired {
		result.Warnings = append(result.Warnings, "⚠️  上诉期已过，需申请再审")
	}

	// Build text summary
	var sb strings.Builder
	sol := result.StatuteOfLimitations
	if sol.Expired {
		sb.WriteString(fmt.Sprintf("❌ %s: 已于 %s 到期（已逾期 %d 天）\n   法律依据: %s\n",
			sol.Label, sol.Cutoff.Format("2006-01-02"), -sol.DaysLeft, sol.LegalBasis))
	} else {
		sb.WriteString(fmt.Sprintf("✅ %s: 截止 %s（还有 %d 天）\n   法律依据: %s\n",
			sol.Label, sol.Cutoff.Format("2006-01-02"), sol.DaysLeft, sol.LegalBasis))
	}
	if result.AppealWindow != nil {
		aw := result.AppealWindow
		if aw.Expired {
			sb.WriteString(fmt.Sprintf("❌ %s: 已于 %s 到期\n   法律依据: %s\n",
				aw.Label, aw.Cutoff.Format("2006-01-02"), aw.LegalBasis))
		} else {
			sb.WriteString(fmt.Sprintf("✅ %s: 截止 %s（还有 %d 天）\n   法律依据: %s\n",
				aw.Label, aw.Cutoff.Format("2006-01-02"), aw.DaysLeft, aw.LegalBasis))
		}
	}
	for _, w := range result.Warnings {
		sb.WriteString(w + "\n")
	}

	return agent.ToolOutput{Text: sb.String(), Data: result}
}
