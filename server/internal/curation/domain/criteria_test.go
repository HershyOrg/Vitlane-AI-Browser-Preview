package domain

import "testing"

func TestCriteriaLargestRemainderAndSemanticIdentity(t *testing.T) {
	c := TargetCriteriaSetV1{SchemaVersion: CriteriaSchema, Version: 1, Subject: ResearchSubject{Label: "만년필", ProductType: "fountain pen"}, Axes: []ResearchAxis{
		{AxisID: "writing", Label: "필기감", Definition: "필기감", Importance: 5, Origin: "REQUEST"},
		{AxisID: "classic", Label: "클래식 디자인", Definition: "클래식 디자인", Importance: 4, Origin: "REQUEST"},
		{AxisID: "value", Label: "가성비", Definition: "가성비", Importance: 3, UsesPrice: true, Origin: "REQUEST"},
	}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	w := c.Weights()
	if w[0] != 42 || w[1] != 33 || w[2] != 25 {
		t.Fatal(w)
	}
	for n := 1; n <= 8; n++ {
		for weight := 1; weight <= 5; weight++ {
			axes := make([]ResearchAxis, n)
			for i := range axes {
				axes[i].Importance = weight
			}
			c2 := TargetCriteriaSetV1{Axes: axes}
			sum := 0
			for _, w := range c2.Weights() {
				sum += w
			}
			if sum != 100 {
				t.Fatal(sum)
			}
		}
	}
	next := c
	next.Axes = append([]ResearchAxis(nil), c.Axes...)
	next.Axes[0].Importance = 2
	if err := c.ValidateSuccessor(next); err != nil {
		t.Fatal(err)
	}
	next.Axes[0].Definition = "changed meaning"
	if c.ValidateSuccessor(next) == nil {
		t.Fatal("reused semantic ID")
	}
}

func TestCriteriaCannotReplaceTargetSubject(t *testing.T) {
	c := TargetCriteriaSetV1{SchemaVersion: CriteriaSchema, Version: 1, Subject: ResearchSubject{Label: "pen", ProductType: "pen"}, Axes: []ResearchAxis{{AxisID: "a", Label: "a", Definition: "a", Importance: 3, Origin: "REQUEST"}}}
	next := c
	next.Subject.ProductType = "chair"
	if c.ValidateSuccessor(next) == nil {
		t.Fatal("target subject replaced")
	}
	next.Subject.ProductType = "pen"
	next.Subject.Label = "Pen"
	if err := c.ValidateSuccessor(next); err != nil {
		t.Fatal(err)
	}
}

func TestWeightRemainderTieUsesSemanticID(t *testing.T) {
	c := TargetCriteriaSetV1{Axes: []ResearchAxis{{AxisID: "b", Importance: 1}, {AxisID: "a", Importance: 1}, {AxisID: "c", Importance: 1}}}
	weights := c.Weights()
	if weights[0] != 33 || weights[1] != 34 || weights[2] != 33 {
		t.Fatal(weights)
	}
}
