package mail

import (
	"fmt"
)

type Mail struct {
	Subject string
	Message string
	To      []string
}

func New() *Mail {
	return &Mail{Subject: "Default Subject"}
}

func (m *Mail) SetSubject(sub string) *Mail {
	m.Subject = sub
	return m
}

func (m *Mail) SetMessage(msg string) *Mail {
	m.Message = msg
	return m
}

func (m *Mail) AddTo(email string) *Mail {
	m.To = append(m.To, email)
	return m
}

func (m *Mail) Send() {
	// Placeholder SMTP logic
	fmt.Printf("Mock Email Sent to %v: %s\n", m.To, m.Subject)
}
