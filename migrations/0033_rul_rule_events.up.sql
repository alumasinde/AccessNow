-- =============================================================================
-- 0033 module / rules_policy — the two events the rulebook publishes
--
-- The gate now consults the tenant's own rulebook on every scan, and a rule that bit is
-- worth somebody knowing about: a refusal the supervisor should see while the person is
-- still at the barrier, and a waiver that was signed for over somebody's restriction.
--
-- rule.matched is new. gate.override_used was seeded by 0025 with a name and a recipient
-- role and has never had a publisher - until this module, which is exactly the thing it
-- was describing. It gets its wording here rather than a new key, because a tenant that
-- already routed it is routed for the real thing now.
--
-- Both are in_app only. A rule that warns every scan of a category would otherwise put an
-- email in a supervisor's inbox for each one, and the notice that matters at a gate is the
-- one on the screen the supervisor is already watching. The evaluation log behind them is
-- the durable record; this is the nudge.
--
-- No index is added for the walk: idx_rules_match (tenant_id, is_active, stage, priority)
-- already covers the only question the candidate lookup asks, and a tenant files dozens of
-- rules, not thousands.
--
-- Re-runnable: INSERT ... ON DUPLICATE KEY UPDATE only, no ALTER TABLE.
-- =============================================================================

INSERT INTO notification_events
    (event_key, name, module_key, default_channels, default_recipient_types, is_opt_out)
VALUES
    ('rule.matched', 'A rule in the tenant rulebook refused or flagged a scan', 'rules_policy',
     JSON_ARRAY('in_app'), JSON_ARRAY('gate_supervisor'), 1),
    ('gate.override_used', 'A gate refusal was waived by somebody with permission', 'rules_policy',
     JSON_ARRAY('in_app'), JSON_ARRAY('gate_supervisor'), 1)
ON DUPLICATE KEY UPDATE
    name                    = VALUES(name),
    module_key              = VALUES(module_key),
    default_channels        = VALUES(default_channels),
    default_recipient_types = VALUES(default_recipient_types),
    is_opt_out              = VALUES(is_opt_out),
    status                  = 'active';

-- {{decision}} is the word the gate answered with - refused, warned, notified or waived -
-- and {{reason}} is the sentence the guard was shown, which is the rule's own failure
-- message rather than anything the module invented. A template that could only say "a rule
-- matched" would be read once and ignored.
INSERT INTO notification_templates
    (tenant_id, event_key, channel, locale, subject, body, short_body, is_html, variables)
VALUES
    (NULL, 'rule.matched', 'in_app', 'en', NULL,
     '{{decision}} at the gate: {{subject_name}} on {{pass_no}}. {{reason}}',
     '{{decision}}: {{pass_no}} - {{reason}}', 0,
     JSON_ARRAY('decision', 'subject_name', 'pass_no', 'reason')),

    (NULL, 'gate.override_used', 'in_app', 'en', NULL,
     '{{actor_name}} waived a gate rule for {{pass_no}}: {{reason}}. Said: {{note}}',
     '{{actor_name}} waived the rule on {{pass_no}}.', 0,
     JSON_ARRAY('actor_name', 'pass_no', 'reason', 'note'))
ON DUPLICATE KEY UPDATE
    body       = VALUES(body),
    short_body = VALUES(short_body),
    variables  = VALUES(variables);
