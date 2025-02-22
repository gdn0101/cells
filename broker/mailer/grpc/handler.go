/*
 * Copyright (c) 2018. Abstrium SAS <team (at) pydio.com>
 * This file is part of Pydio Cells.
 *
 * Pydio Cells is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * Pydio Cells is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with Pydio Cells.  If not, see <http://www.gnu.org/licenses/>.
 *
 * The latest code can be found at <https://pydio.com>.
 */

package grpc

import (
	"context"
	"fmt"

	json "github.com/pydio/cells/x/jsonx"

	protobuf "github.com/golang/protobuf/proto"
	"github.com/matcornic/hermes"
	"github.com/micro/go-micro/errors"
	"go.uber.org/zap"

	"github.com/pydio/cells/broker/mailer"
	"github.com/pydio/cells/broker/mailer/templates"
	"github.com/pydio/cells/common"
	"github.com/pydio/cells/common/config"

	// "github.com/pydio/cells/common/forms"
	"github.com/pydio/cells/common/log"
	proto "github.com/pydio/cells/common/proto/mailer"
	servicecontext "github.com/pydio/cells/common/service/context"
	"github.com/pydio/cells/x/configx"
)

type Handler struct {
	queueName    string
	queueConfig  configx.Values
	senderName   string
	senderConfig configx.Values
	queue        mailer.Queue
	sender       mailer.Sender
}

func NewHandler(serviceCtx context.Context, conf configx.Values) (*Handler, error) {
	h := new(Handler)
	h.initFromConf(serviceCtx, conf, true)
	return h, nil
}

// SendMail either queues or send a mail directly
func (h *Handler) SendMail(ctx context.Context, req *proto.SendMailRequest, rsp *proto.SendMailResponse) error {
	mail := req.Mail

	// Sanity checks
	if mail == nil || (len(mail.Subject) == 0 && mail.TemplateId == "") || len(mail.To) == 0 {
		e := errors.BadRequest(common.ServiceMailer, "cannot send mail: some required fields are missing")
		log.Logger(ctx).Error("cannot process mail to send", zap.Any("Mail", mail), zap.Error(e))
		return e
	}
	if mail.ContentPlain == "" && mail.ContentMarkdown == "" && mail.ContentHtml == "" && mail.TemplateId == "" {
		e := errors.BadRequest(common.ServiceMailer, "SendMail: please provide one of ContentPlain, ContentMarkdown or ContentHtml")
		log.Logger(ctx).Error("cannot process mail to send: empty body", zap.Any("Mail", mail), zap.Error(e))
		return e
	}

	h.checkConfigChange(ctx, false)

	for _, to := range mail.To {
		// Find language to be used
		var languages []string
		if to.Language != "" {
			languages = append(languages, to.Language)
		}
		configs := templates.GetApplicationConfig(languages...)
		// Clone email and set unique user
		m := protobuf.Clone(mail).(*proto.Mail)
		m.To = []*proto.User{to}
		if configs.FromCtl == "default" {
			if m.From == nil {
				m.From = &proto.User{
					Address: configs.From,
					Name:    configs.FromName,
				}
			} else {
				m.From.Address = configs.From
				m.From.Name = configs.FromName
			}
		} else if configs.FromCtl == "sender" {
			if m.From == nil {
				m.From = &proto.User{
					Address: configs.From,
					Name:    configs.FromName,
				}
			} else if m.From.Address == "" {
				m.From.Address = configs.From
			}
			if m.From.Address != configs.From {
				m.Sender = &proto.User{
					Address: configs.From,
					Name:    configs.FromName,
				}
			}
		} else {
			if m.From == nil {
				m.From = &proto.User{
					Address: configs.From,
					Name:    configs.FromName,
				}
			} else if m.From.Address == "" {
				m.From.Address = configs.From
			}
		}
		he := templates.GetHermes(languages...)
		if m.ContentHtml == "" {
			var body hermes.Body
			if m.TemplateId != "" {
				var subject string
				subject, body = templates.BuildTemplateWithId(to, m.TemplateId, m.TemplateData, languages...)
				m.Subject = subject
				if m.ContentMarkdown != "" {
					body.FreeMarkdown = hermes.Markdown(m.ContentMarkdown)
				}
			} else {
				if m.ContentMarkdown != "" {
					body = hermes.Body{
						FreeMarkdown: hermes.Markdown(m.ContentMarkdown),
					}
				} else {
					body = hermes.Body{
						Intros: []string{m.ContentPlain},
					}
				}
			}
			hermesMail := hermes.Email{Body: body}
			var e error
			m.ContentHtml, _ = he.GenerateHTML(hermesMail)
			if m.ContentPlain, e = he.GenerateHTML(hermesMail); e != nil {
				return e
			}
		}

		// Restrict number of logged To
		tt := m.To
		if len(tt) > 20 {
			tt = tt[:20]
		}
		if req.InQueue {
			log.Logger(ctx).Debug("SendMail: pushing email to queue", log.DangerouslyZapSmallSlice("to", tt), zap.Any("from", m.From), zap.Any("subject", m.Subject))
			if e := h.queue.Push(m); e != nil {
				log.Logger(ctx).Error(fmt.Sprintf("cannot put mail in queue: %s", e.Error()), log.DangerouslyZapSmallSlice("to", tt), zap.Any("from", m.From), zap.Any("subject", m.Subject))
				return e
			}
		} else {
			log.Logger(ctx).Info("SendMail: sending email", log.DangerouslyZapSmallSlice("to", tt), zap.Any("from", m.From), zap.Any("subject", m.Subject))
			if e := h.sender.Send(m); e != nil {
				log.Logger(ctx).Error(fmt.Sprintf("could not directly send mail: %s", e.Error()), log.DangerouslyZapSmallSlice("to", tt), zap.Any("from", m.From), zap.Any("subject", m.Subject))
				return e
			}
		}
	}
	return nil
}

// ConsumeQueue browses current queue for emails to be sent
func (h *Handler) ConsumeQueue(ctx context.Context, req *proto.ConsumeQueueRequest, rsp *proto.ConsumeQueueResponse) error {

	h.checkConfigChange(ctx, false)

	counter := int64(0)
	c := func(em *proto.Mail) error {
		if em == nil {
			log.Logger(ctx).Error("ConsumeQueue: trying to send empty email")
			return fmt.Errorf("cannot send empty email")
		}
		counter++
		return h.sender.Send(em)
	}

	e := h.queue.Consume(c)
	if e != nil {
		return e
	}

	rsp.Message = fmt.Sprintf("Successfully sent %d messages", counter)
	log.TasksLogger(ctx).Info(rsp.Message)
	rsp.EmailsSent = counter
	return nil
}

func (h *Handler) parseConf(conf configx.Values) (queueName string, queueConfig configx.Values, senderName string, senderConfig configx.Values) {

	// Defaults
	queueName = "boltdb"
	senderName = "sendmail"
	senderConfig = conf.Val("sender")
	queueConfig = conf.Val("queue")

	queueName = queueConfig.Val("@value").Default("boltdb").String()
	senderName = senderConfig.Val("@value").Default("sendmail").String()

	return
}

func (h *Handler) initFromConf(ctx context.Context, conf configx.Values, check bool) (e error) {

	initialConfig := conf.Val("valid").Bool()
	defer func() {
		var newConfig bool
		if e != nil {
			newConfig = false
		} else {
			newConfig = true
		}
		if newConfig != initialConfig {
			//config.Get("services", servicecontext.GetServiceName(ctx), "valid").Set(true)
			conf.Val("valid").Set(newConfig)
			config.Save(common.PydioSystemUsername, "Update mailer valid config")
		}
	}()

	queueName, queueConfig, senderName, senderConfig := h.parseConf(conf)
	if h.queue != nil {
		h.queue.Close()
	}
	h.queue = mailer.GetQueue(ctx, queueName, queueConfig)
	if h.queue == nil {
		queueName = "boltdb"
		h.queue = mailer.GetQueue(ctx, "boltdb", conf)
	} else {
		log.Logger(ctx).Info("Starting mailer with queue '" + queueName + "'")
	}

	sender, err := mailer.GetSender(ctx, senderName, senderConfig)
	if err != nil {
		e = err
		return
	}

	log.Logger(ctx).Info("Starting mailer with sender '" + senderName + "'")
	h.sender = sender
	h.queueName = queueName
	h.queueConfig = queueConfig
	h.senderName = senderName
	h.senderConfig = senderConfig

	if check {
		e = h.sender.Check(ctx)
	}

	return
}

func (h *Handler) checkConfigChange(ctx context.Context, check bool) error {

	cfg := config.Get("services", servicecontext.GetServiceName(ctx))
	queueName, _, senderName, senderConfig := h.parseConf(cfg)
	m1, _ := json.Marshal(senderConfig)
	m2, _ := json.Marshal(h.senderConfig)

	if queueName != h.queueName || senderName != h.senderName || string(m1) != string(m2) {
		log.Logger(ctx).Info("Mailer configuration has changed. Refreshing sender and queue")
		return h.initFromConf(ctx, cfg, check)
	}
	return nil
}
