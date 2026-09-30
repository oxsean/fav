-- A delivery's result and a webhook test's audit keep only a class of what happened: an HTTP status, one of the
-- classes below, or the outbox's own reason. An error's text can name the address, whose path is a device's secret.
UPDATE deliveries SET result = CASE
	WHEN result = 'Post: timeout' THEN 'timeout'
	WHEN result = 'dial: connect: connection refused' THEN 'connect'
	WHEN result = 'dial: no such host' THEN 'dns'
	ELSE 'unreachable'
END WHERE result NOT IN ('', 'ok', 'timeout', 'dns', 'connect', 'tls', 'refused', 'unreachable', 'no device', 'another owner',
	'signed out', 'no webhook', 'bad notice', 'no channel webhook', 'no channel webpush', 'removed', 'dropped', 'expired')
	AND NOT (length(result) = 3 AND result GLOB '[1-5][0-9][0-9]');
UPDATE audit SET detail = CASE
	WHEN detail = 'Post: timeout' THEN 'timeout'
	WHEN detail = 'dial: connect: connection refused' THEN 'connect'
	WHEN detail = 'dial: no such host' THEN 'dns'
	ELSE 'unreachable'
END WHERE kind = 'webhook.test' AND detail NOT IN ('ok', 'timeout', 'dns', 'connect', 'tls', 'refused', 'unreachable')
	AND NOT (length(detail) = 3 AND detail GLOB '[1-5][0-9][0-9]');
