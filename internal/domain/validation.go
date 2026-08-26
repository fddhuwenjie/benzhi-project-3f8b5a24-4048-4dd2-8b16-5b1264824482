package domain

func ValidStatus(s Status) bool {
	switch s {
	case Draft, PlanReady, Conditioned, Tested, ReviewPending, Released, Sealed:
		return true
	}
	return false
}
