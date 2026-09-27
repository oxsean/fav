package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/wire"
)

// answers is a repeatable --answer flag.
type answers []string

func (a *answers) String() string     { return strings.Join(*a, ", ") }
func (a *answers) Set(v string) error { *a = append(*a, v); return nil }

// cmdRunAnswer is `run answer <run>`: allow or deny what the run waits on, or answer its questions.
func cmdRunAnswer(args []string) error {
	fs := newFlags("run")
	request := fs.String("request", "", i18n.T("cli.run.flag_request"))
	allow := fs.Bool("allow", false, i18n.T("cli.run.flag_allow"))
	deny := fs.Bool("deny", false, i18n.T("cli.run.flag_deny"))
	message := fs.String("message", "", i18n.T("cli.run.flag_deny_message"))
	var given answers
	fs.Var(&given, "answer", i18n.T("cli.run.flag_answer"))
	pos, err := parseWithArgs(fs, args, 1)
	if err != nil {
		return err
	}
	return withCoord(wire.Options{}, func(cl *coord.Client) error {
		st, err := readState(cl)
		if err != nil {
			return err
		}
		id, err := runID(st, pos[0])
		if err != nil {
			return err
		}
		r := st.Runs[id]
		if len(r.Requests) == 0 {
			return i18n.E("cli.run.nothing_waits", id)
		}
		req := r.Requests[0]
		if *request != "" {
			i := slices.IndexFunc(r.Requests, func(q agent.Request) bool { return q.ID == *request })
			if i < 0 {
				return i18n.E("cli.run.no_request", *request, id)
			}
			req = r.Requests[i]
		}
		a := agent.Answer{Request: req.ID, Allow: !*deny, Message: *message}
		switch {
		case *deny && (*allow || len(given) > 0):
			return errors.New(i18n.T("cli.run.answer_one_way"))
		case req.Kind == agent.RequestQuestion && !*deny:
			if a.Answers, err = answerQuestions(req.Questions, given); err != nil {
				return err
			}
		case req.Kind == agent.RequestPermission && !*allow && !*deny:
			return errors.New(i18n.T("cli.run.answer_allow_or_deny"))
		}
		var out task.Run
		if err := write(cl, coord.MRunAnswer, coord.Answer{Run: id, Answer: a}, &out); err != nil {
			return err
		}
		fmt.Print(i18n.F("cli.run.answered", id))
		return nil
	})
}

// answerQuestions maps each --answer to its question: "QUESTION=ANSWER", or a bare answer when there is one question.
func answerQuestions(qs []agent.Question, given []string) (map[string]string, error) {
	out := map[string]string{}
	for _, g := range given {
		q, a, ok := strings.Cut(g, "=")
		if !ok && len(qs) == 1 {
			q, a = qs[0].Question, g
		}
		if !slices.ContainsFunc(qs, func(x agent.Question) bool { return x.Question == q }) {
			return nil, i18n.E("cli.run.unknown_question", q)
		}
		out[q] = strings.TrimSpace(a)
	}
	for _, q := range qs {
		if out[q.Question] == "" {
			return nil, i18n.E("cli.run.need_answer", q.Question)
		}
	}
	return out, nil
}

// cmdRunSend is `run send <run> <text>`: a message for a run while it runs.
func cmdRunSend(args []string) error {
	fs := newFlags("run")
	file := fs.String("file", "", i18n.T("cli.run.flag_text_file"))
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return i18n.E("cli.run.need_text")
	}
	ref, text := pos[0], strings.Join(pos[1:], " ")
	if *file != "" {
		if text, err = readBrief("", *file); err != nil {
			return err
		}
	}
	if strings.TrimSpace(text) == "" {
		return i18n.E("cli.run.need_text")
	}
	return withCoord(wire.Options{}, func(cl *coord.Client) error {
		st, err := readState(cl)
		if err != nil {
			return err
		}
		id, err := runID(st, ref)
		if err != nil {
			return err
		}
		var out task.Run
		if err := write(cl, coord.MRunSend, coord.SendMessage{Run: id, Text: text}, &out); err != nil {
			return err
		}
		fmt.Print(i18n.F("cli.run.sent", id))
		return nil
	})
}

// requestLine is what a run waits on, as one line.
func requestLine(q agent.Request) string {
	if q.Kind == agent.RequestQuestion {
		var parts []string
		for _, x := range q.Questions {
			parts = append(parts, x.Question+" ["+strings.Join(x.Options, " / ")+"]")
		}
		return i18n.F("cli.run.show_question", strings.Join(parts, "; "), q.ID)
	}
	return i18n.F("cli.run.show_permission", q.Tool, render.Sanitize(q.Summary), q.ID)
}
