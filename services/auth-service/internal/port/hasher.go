package port

type Hasher interface {
	Hash(password string) (string, error)
	Compare(hash, password string) error
}
