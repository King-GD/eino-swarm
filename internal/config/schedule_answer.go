package config

import "strings"

const (
	ScheduleAnswerAuto         = "auto"
	ScheduleAnswerDecision     = "decision"
	ScheduleAnswerChat         = "chat"
	DefaultScheduleAnswerModel = ScheduleAnswerChat
	// Only a low probability of a concrete scheduling claim skips extraction.
	ScheduleAnswerDecisionThreshold = 0.1
)

func NormalizeScheduleAnswerModel(value string) string {
	switch mode := strings.ToLower(strings.TrimSpace(value)); mode {
	case ScheduleAnswerAuto, ScheduleAnswerDecision, ScheduleAnswerChat:
		return mode
	default:
		return DefaultScheduleAnswerModel
	}
}
