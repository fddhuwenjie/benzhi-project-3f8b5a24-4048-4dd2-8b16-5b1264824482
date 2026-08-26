package store

func (s *Store) Count() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.cases) }
