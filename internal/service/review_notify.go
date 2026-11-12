package service

// The review's push channel (ADR-0053 D7, the `review.notify` row).
//
// There is no OS notification in this app and deliberately so: Cinder is a
// workbench the user opens, not a daemon that taps them on the shoulder, and
// every platform's notification permission prompt is a decision the user would
// have to make before seeing any value. The channel is the rail — a dot on the
// Code Review entry, the same mechanism the update check already uses.
//
// The counting rules (what "high" means, why an unverified candidate must not
// turn the dot on) live in reviewrun/notify.go beside the pipeline that
// produces them.

// ReviewNotifyCount returns how many findings the current threshold says are
// worth a mark on the rail. Zero is the common answer and means "do not
// disturb".
func (s *Service) ReviewNotifyCount() (int, error) {
	return s.reviewRunner().NotifyCount()
}
