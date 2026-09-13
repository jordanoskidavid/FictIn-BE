package auth

import "golang.org/x/crypto/bcrypt"

// Hasher wraps bcrypt with a fixed cost.
type Hasher struct {
	cost int
}

func NewHasher(cost int) *Hasher {
	if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
		cost = bcrypt.DefaultCost
	}
	return &Hasher{cost: cost}
}

func (h *Hasher) Hash(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), h.cost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Compare reports whether plain matches the stored hash.
func (h *Hasher) Compare(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}
