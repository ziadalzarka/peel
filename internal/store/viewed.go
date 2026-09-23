package store

type ViewedStore interface {
	Load() ([]string, error)
	Update(apply func(viewed map[string]bool) error) error
}
