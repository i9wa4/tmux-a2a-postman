package config

const (
	directTemplateRootNotification         = "notification_template"
	directTemplateRootDaemonMessage        = "daemon_message_template"
	directTemplateRootDraft                = "draft_template"
	directTemplateRootEdgeViolationWarning = "edge_violation_warning_template"
	directTemplateRootMessageFooter        = "message_footer"
)

func (cfg *Config) initDirectTemplateRootTrust() {
	cfg.directTemplateRootTrust = map[string]bool{
		directTemplateRootNotification:         true,
		directTemplateRootDaemonMessage:        true,
		directTemplateRootDraft:                true,
		directTemplateRootEdgeViolationWarning: true,
		directTemplateRootMessageFooter:        true,
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

func (cfg *Config) AllowShellForEdgeViolationWarningTemplate() bool {
	return cfg.allowShellForDirectTemplateRoot(directTemplateRootEdgeViolationWarning)
}

func (cfg *Config) AllowShellForMessageFooter() bool {
	return cfg.allowShellForDirectTemplateRoot(directTemplateRootMessageFooter)
}
