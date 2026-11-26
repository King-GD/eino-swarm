export const scheduleAnswerSettingsEn = {
  "settings.swarm.scheduleAnswerCheck": "Check scheduling claims in answers",
  "settings.swarm.scheduleAnswerCheckHint": "Check scheduling claims before publishing an answer. Turning this off lets the answer through without verifying those claims.",
  "settings.swarm.scheduleAnswerModel": "Answer check model",
  "settings.swarm.scheduleAnswerModelHint": "Automatic uses the Gateway decision model when available, then checks possible claims with the chat model. Choose Decision model to require Gateway support.",
  "settings.swarm.scheduleAnswerAuto": "Automatic",
  "settings.swarm.scheduleAnswerDecision": "Decision model",
  "settings.swarm.scheduleAnswerChat": "Follow chat model",
  "notice.scheduleAnswerDecisionUnavailable": "The answer requires a Gateway decision model. Choose Automatic or Follow chat model in Swarm settings if this conversation cannot use one.",
}

export const scheduleAnswerSettingsZh: { [K in keyof typeof scheduleAnswerSettingsEn]: string } = {
  "settings.swarm.scheduleAnswerCheck": "核对答复中的调度声明",
  "settings.swarm.scheduleAnswerCheckHint": "发布答复前核对其中的调度声明。关闭后，答复里的调度声明会直接发布。",
  "settings.swarm.scheduleAnswerModel": "答复核对模型",
  "settings.swarm.scheduleAnswerModelHint": "自动优先使用网关决策模型初筛，疑似存在调度声明时用聊天模型提取详情。选择决策模型需要网关支持。",
  "settings.swarm.scheduleAnswerAuto": "自动",
  "settings.swarm.scheduleAnswerDecision": "决策模型",
  "settings.swarm.scheduleAnswerChat": "跟随聊天模型",
  "notice.scheduleAnswerDecisionUnavailable": "答复核对需要网关决策模型。当前会话无法使用时，可在集群设置中选择「自动」或「跟随聊天模型」。",
}
