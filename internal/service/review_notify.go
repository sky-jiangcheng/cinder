package service

// The review's push channel (ADR-0053 D7, the `review.notify` row).
//
// There is no OS notification in this app and deliberately so: Cinder is a
// workbench the user opens, not a daemon that taps them on the shoulder, and
// every platform's notification permission prompt is a decision the user would
// have to make before seeing any value. The channel is the rail — a dot on the
// Code Review entry, the same mechanism the update check already uses.
//
// Two rules follow from the ADR and are enforced here rather than in the UI,
// because a UI that forgets them would nag:
//
//  1. `high` is the default threshold and it means `severity=high AND
//     verdict=confirmed`. A rule that fired but no model checked it
//     (`unverified`) is a candidate, not a finding — the ADR measures
//     precision before trusting it, so it must not turn the dot on.
//  2. Nothing qualifying means no dot at all. "Reviewed, nothing found" is
//     not a message worth the user's attention.

// ReviewNotifyCount returns how many findings the current threshold says are
// worth a mark on the rail. Zero is the common answer and means "do not
// disturb".
func (s *Service) ReviewNotifyCount() (int, error) {
	cfg, err := s.GetReviewConfig()
	if err != nil {
		return 0, err
	}
	// The master switch means the feature is off entirely: no scheduled run
	// and no rail dot, regardless of the notify threshold (ADR-0053 D5).
	if !cfg.Enabled {
		return 0, nil
	}
	if cfg.Notify == "off" {
		return 0, nil
	}
	run, findings, err := s.LatestReview(nil, nil)
	if err != nil || run == nil {
		return 0, err
	}
	if cfg.Notify == "all" {
		return len(findings), nil
	}
	n := 0
	for _, f := range findings {
		if f.Severity == "high" && f.Verdict == "confirmed" {
			n++
		}
	}
	return n, nil
}
