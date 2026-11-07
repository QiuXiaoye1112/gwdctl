package data

import (
	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
	"github.com/OpenListTeam/OpenList/v4/internal/message"
	log "github.com/sirupsen/logrus"
)

func initDevData() {
}

func initDevDo() {
	if flags.Dev {
		go func() {
			err := message.GetMessenger().WaitSend(message.Message{
				Type:    "string",
				Content: "dev mode",
			}, 10)
			if err != nil {
				log.Debugf("%+v", err)
			}
			m, err := message.GetMessenger().WaitReceive(10)
			if err != nil {
				log.Debugf("%+v", err)
			} else {
				log.Debugf("received: %+v", m)
			}
		}()
	}
}
