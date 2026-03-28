package config

const (
	directTemplateRootNotification         = "notification_template"
	directTemplateRootDaemonMessage        = "daemon_message_template"
	directTemplateRootDraft                = "draft_template"
	directTemplateRootGlobalReminder       = "reminder_message"
	directTemplateRootEdgeViolationWarning = "edge_violation_warning_template"
	directTemplateRootDroppedBallEvent     = "dropped_ball_event_template"
	directTemplateRootMessageFooter        = "message_footer"
)

func reminderTemplateRoot(nodeName string) string {
	return "node:" + nodeName + ":reminder_message"
}

func (cfg *Config) initDirectTemplateRootTrust() {
	cfg.directTemplateRootTrust = map[string]bool{
		directTemplateRootNotification:         true,
		directTemplateRootDaemonMessage:        true,
		directTemplateRootDraft:                true,
		directTemplateRootGlobalReminder:       true,
		directTemplateRootEdgeViolationWarning: true,
		directTemplateRootDroppedBallEvent:     true,
		directTemplateRootMessageFooter:        true,
	}
	for name, node := range cfg.Nodes {
		if node.ReminderMessage != "" {
			cfg.directTemplateRootTrust[reminderTemplateRoot(name)] = true
		}
	}
}

func (cfg *Config) markDirectTemplateRootsUntrusted(override *Config) {
	if cfg == nil || override == nil || cfg.directTemplateRootTrust == nil {
		return
	}

	if override.NotificationTemplate != "" {
		cfg.directTemplateRootTrust[directTemplateRootNotification] = false
	}
	if override.DaemonMessageTemplate != "" {
		cfg.directTemplateRootTrust[directTemplateRootDaemonMessage] = false
	}
	if override.DraftTemplate != "" {
		cfg.directTemplateRootTrust[directTemplateRootDraft] = false
	}
	if override.ReminderMessage != "" {
		cfg.directTemplateRootTrust[directTemplateRootGlobalReminder] = false
	}
	if override.EdgeViolationWarningTemplate != "" {
		cfg.directTemplateRootTrust[directTemplateRootEdgeViolationWarning] = false
	}
	if override.DroppedBallEventTemplate != "" {
		cfg.directTemplateRootTrust[directTemplateRootDroppedBallEvent] = false
	}
	if override.MessageFooter != "" {
		cfg.directTemplateRootTrust[directTemplateRootMessageFooter] = false
	}
	for name, node := range override.Nodes {
		if node.ReminderMessage != "" {
			cfg.directTemplateRootTrust[reminderTemplateRoot(name)] = false
		}
	}
}

func (cfg *Config) allowShellForDirectTemplateRoot(root string) bool {
	if cfg == nil || !cfg.AllowShellTemplates {
		return false
	}
	if cfg.directTemplateRootTrust == nil {
		return true
	}
	allowed, ok := cfg.directTemplateRootTrust[root]
	if !ok {
		return true
	}
	return allowed
}

func (cfg *Config) AllowShellForNotificationTemplate() bool {
	return cfg.allowShellForDirectTemplateRoot(directTemplateRootNotification)
}

func (cfg *Config) AllowShellForDaemonMessageTemplate() bool {
	return cfg.allowShellForDirectTemplateRoot(directTemplateRootDaemonMessage)
}

func (cfg *Config) AllowShellForDraftTemplate() bool {
	return cfg.allowShellForDirectTemplateRoot(directTemplateRootDraft)
}

func (cfg *Config) AllowShellForReminderMessage(nodeName string) bool {
	if cfg == nil {
		return false
	}
	if node, ok := cfg.Nodes[nodeName]; ok && node.ReminderMessage != "" {
		return cfg.allowShellForDirectTemplateRoot(reminderTemplateRoot(nodeName))
	}
	return cfg.allowShellForDirectTemplateRoot(directTemplateRootGlobalReminder)
}

func (cfg *Config) AllowShellForEdgeViolationWarningTemplate() bool {
	return cfg.allowShellForDirectTemplateRoot(directTemplateRootEdgeViolationWarning)
}

func (cfg *Config) AllowShellForDroppedBallEventTemplate() bool {
	return cfg.allowShellForDirectTemplateRoot(directTemplateRootDroppedBallEvent)
}

func (cfg *Config) AllowShellForMessageFooter() bool {
	return cfg.allowShellForDirectTemplateRoot(directTemplateRootMessageFooter)
}
