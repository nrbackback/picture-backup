package email

import "gopkg.in/gomail.v2"

type EmailConfig struct {
	From     string `yaml:"from"`
	Password string `yaml:"password"`
	Server   string `yaml:"server"`
	Port     int    `yaml:"port"`
	To       string `yaml:"to"`
}

type EmailSender struct {
	Conf EmailConfig
}

func (e EmailSender) Send(title, content string) error {
	m := gomail.NewMessage()
	m.SetHeader("From", e.Conf.From)
	m.SetHeader("To", e.Conf.To)

	m.SetHeader("Subject", title)
	m.SetBody("text/html", content)
	d := gomail.NewPlainDialer(e.Conf.Server, e.Conf.Port, e.Conf.From, e.Conf.Password)
	err := d.DialAndSend(m)
	return err
}
