package loop

type Role string

type Message struct {
	Role    Role
	Content string
}
