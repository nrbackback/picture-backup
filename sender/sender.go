package sender

type Sender interface {
	Send(title, content string) error
}
