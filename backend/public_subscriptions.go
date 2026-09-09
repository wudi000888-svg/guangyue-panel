package main

func (s *Store) migratePublicSubscriptions() error {
	records, err := s.records()
	if err != nil {
		return err
	}
	for _, r := range records {
		if r.Credentials.PublicToken == "" {
			if err = s.save(&r); err != nil {
				return err
			}
		}
	}
	return nil
}

func scopedSubscription(c Config, r Record, nodes []Node, format, protocol string, public bool) ([]byte, string, error) {
	selected := []Node{}
	for _, n := range nodes {
		if (n.ManagedBy == publicManager) == public {
			selected = append(selected, n)
		}
	}
	return renderSubscription(c, r, selected, format, protocol, public)
}
