package main

import (
	"testing"

	"github.com/oxsean/fav/internal/agent"
)

func TestAnswersMapToTheirQuestions(t *testing.T) {
	one := []agent.Question{{Question: "Which DB?"}}
	if got, err := answerQuestions(one, []string{"pg"}); err != nil || got["Which DB?"] != "pg" {
		t.Fatalf("a bare answer to the only question: %v %v", got, err)
	}
	two := []agent.Question{{Question: "Which DB?"}, {Question: "Port=?"}}
	if got, err := answerQuestions(two, []string{"Which DB?=pg", "Port=?=5432"}); err == nil || got != nil {
		t.Fatalf("an answer names its question up to the first '=': %v %v", got, err)
	}
	if _, err := answerQuestions(two, []string{"Which DB?=pg"}); err == nil {
		t.Fatal("every question needs an answer")
	}
	if _, err := answerQuestions(one, []string{"Other?=x", "pg"}); err == nil {
		t.Fatal("a question it did not ask")
	}
}
