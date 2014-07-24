// Copyright (c) 2014, Kevin Walsh.  All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// This code borrows heavily from the lexer design and implementation for the
// template package. See http://golang.org/src/pkg/text/template/parse/parse.go

// Engine for a text-based Datalog interpreter. Also provides pretty-printing
// for datalog literals, predicates, etc.
package dlengine

import (
	"fmt"
	"strconv"

	"datalog"
)

// Var represents a variable with a name, e.g. X, Y. Name should start with
// uppercase and follow traditional datalog syntax.
type Var struct {
	Name string
	datalog.DistinctVar
}

// NewVar returns a Var with the given name.
func NewVar(name string) *Var {
	v := new(Var)
	v.Name = name
	return v
}

func (v *Var) String() string {
	return v.Name
}

// Quoted represents a quoted string constant, e.g. "Alice", "Hello\nWorld".
type Quoted struct {
	Value string
	datalog.DistinctConst
}

func (q *Quoted) String() string {
	return strconv.Quote(q.Value)
}

// NewQuoted returns a Quoted with the given value.
func NewQuoted(value string) *Quoted {
	q := new(Quoted)
	q.Value = value
	return q
}

// Ident represents a bare identifier constant, e.g. alice, -42. Value should
// start with non-uppercase and follow traditional datalog syntax.
type Ident struct {
	Value string
	datalog.DistinctConst
}

func (i *Ident) String() string {
	return i.Value
}

// NewIdent returns an Ident with the given value.
func NewIdent(value string) *Ident {
	i := new(Ident)
	i.Value = value
	return i
}

// Pred represents a database-defined predicate with a name and arity, e.g.
// ancestor/2. Name should start with non-uppercase and follow traditional
// datalog syntax.
type Pred struct {
	Name string
	datalog.DBPred
}

func (p *Pred) String() string {
	return p.Name
}

// NewPred returns a Pred with the given name and arity.
func NewPred(name string, arity int) *Pred {
	p := new(Pred)
	p.Name = name
	p.SetArity(arity)
	return p
}

// NewRule returns a new clause with the given head and body literals.
func NewRule(head *datalog.Literal, body ...*datalog.Literal) *datalog.Clause {
	return &datalog.Clause{Head: head, Body: body}
}

// Engine maintains state for the datalog prover. The main task of the engine is
// to map a given piece of text to existing Var, Ident, Quoted, and Pred
// objects. Because go does not provide weak references, reference counting is
// needed to ensure that objects that are no longer used are removed from the
// Engine to be garbage collected.
type Engine struct {
	Term     map[string]datalog.Term // live variables, constants, and identifiers
	Pred     map[string]datalog.Pred // live predicates
	refCount map[interface{}]int
}

// Construct a new engine.
func NewEngine() *Engine {
	return &Engine{
		Term:     make(map[string]datalog.Term),
		Pred:     make(map[string]datalog.Pred),
		refCount: make(map[interface{}]int),
	}
}

// Add the given predicate to the engine. This can be used to add custom
// predicates like Equals to the engine. It can also be used to add the same
// predicate to multiple engines (they will then share state for that
// predicate). Any previous predicate with same name is replaced.
func (e *Engine) AddPred(p datalog.Pred) {
	id := fmt.Sprintf("%v", p) + "/" + strconv.Itoa(p.Arity())
	e.Pred[id] = p
}

func (e *Engine) Process(name, input string) (assertions, retractions, queries, errors int) {
	pgm, err := parse(name, input)
	if err != nil {
		errors++
		fmt.Printf("datalog: %s", err.Error())
		return
	}
	for _, node := range pgm.nodeList {
		switch node := node.(type) {
		case *actionNode:
			if node.action == actionAssert {
				err = e.assert(node.clause, true)
				assertions++
			} else {
				err = e.retract(node.clause, true)
				retractions++
			}
		case *queryNode:
			err = e.query(node.literal)
			queries++
		default:
			panic("not reached")
		}
		if err != nil {
			fmt.Printf("datalog: %s:%d: %s\n", name, node.Position(), err.Error())
			errors++
		} else {
			fmt.Printf("OK\n")
		}
	}
	return
}

func (e *Engine) Batch(name, input string) (assertions, retractions int, err error) {
	pgm, err := parse(name, input)
	if err != nil {
		return
	}
	for _, node := range pgm.nodeList {
		switch node := node.(type) {
		case *actionNode:
			if node.action == actionAssert {
				err = e.assert(node.clause, false)
				assertions++
			} else {
				err = e.retract(node.clause, false)
				retractions++
			}
		case *queryNode:
			// ignore
		default:
			panic("not reached")
		}
		if err != nil {
			return
		}
	}
	return
}

func (e *Engine) assert(clause *clauseNode, interactive bool) error {
	c := e.recoverClause(clause)
	if interactive {
		fmt.Printf("Assert: %s\n", c)
	}
	err := c.Assert()
	e.track(c, +1)
	return err
}

func (e *Engine) retract(clause *clauseNode, interactive bool) error {
	c := e.recoverClause(clause)
	if interactive {
		fmt.Printf("Retract: %s\n", c)
	}
	err := c.Retract()
	e.track(c, -1)
	return err
}

func (e *Engine) query(literal *literalNode) error {
	l := e.recoverLiteral(literal)
	fmt.Printf("Query: %s\n", l)
	a := l.Query()
	fmt.Println(a)
	return nil
}

func (e *Engine) Assert(assertion string) error {
	pgm, err := parse("assert", assertion)
	if err != nil {
		return err
	}
	if len(pgm.nodeList) != 1 {
		return fmt.Errorf("datalog: expecting one assertion: %s", assertion)
	}
	node, ok := pgm.nodeList[0].(*actionNode)
	if !ok {
		return fmt.Errorf("datalog: expecting assertion: %s", assertion)
	}
	return e.assert(node.clause, false)
}

func (e *Engine) Retract(retraction string) error {
	pgm, err := parse("retract", retraction)
	if err != nil {
		return err
	}
	if len(pgm.nodeList) != 1 {
		return fmt.Errorf("datalog: expecting one retraction: %s", retraction)
	}
	node, ok := pgm.nodeList[0].(*actionNode)
	if !ok {
		return fmt.Errorf("datalog: expecting retraction: %s", retraction)
	}
	return e.retract(node.clause, false)
}

func (e *Engine) Query(query string) (datalog.Answers, error) {
	pgm, err := parse("query", query)
	if err != nil {
		return nil, err
	}
	if len(pgm.nodeList) != 1 {
		return nil, fmt.Errorf("datalog: expecting one query: %s", query)
	}
	node, ok := pgm.nodeList[0].(*queryNode)
	if !ok {
		return nil, fmt.Errorf("datalog: expecting query: %s", query)
	}
	l := e.recoverLiteral(node.literal)
	return l.Query(), nil
}

func (e *Engine) recoverClause(clause *clauseNode) *datalog.Clause {
	head := e.recoverLiteral(clause.head)
	body := make([]*datalog.Literal, len(clause.nodeList))
	for i, node := range clause.nodeList {
		body[i] = e.recoverLiteral(node.(*literalNode))
	}
	return NewRule(head, body...)
}

func (e *Engine) recoverLiteral(literal *literalNode) *datalog.Literal {
	name := literal.predsym
	arity := len(literal.nodeList)
	id := name + "/" + strconv.Itoa(arity)
	p, ok := e.Pred[id]
	if !ok {
		fmt.Println("making new pred for ", id)
		p = NewPred(name, arity)
		e.Pred[id] = p
	}
	arg := make([]datalog.Term, arity)
	for i, n := range literal.nodeList {
		leaf := n.(*leafNode)
		fmt.Printf("recovering new leaf term: %v\n", leaf)
		t, ok := e.Term[leaf.val]
		if !ok {
			fmt.Printf("not found, making %v\n", n.Type())
			switch n.Type() {
			case nodeIdentifier:
				t = NewIdent(leaf.val)
			case nodeString:
				t = NewQuoted(leaf.val)
			case nodeVariable:
				t = NewVar(leaf.val)
			default:
				panic("not reached")
			}
			e.Term[leaf.val] = t
		}
		arg[i] = t
	}
	return datalog.NewLiteral(p, arg...)
}

func (e *Engine) track(c *datalog.Clause, inc int) {
	e.trackLiteral(c.Head, inc)
	for _, l := range c.Body {
		e.trackLiteral(l, inc)
	}
}

func (e *Engine) trackLiteral(l *datalog.Literal, inc int) {
	e.trackObject(l.Pred, inc)
	for _, t := range l.Arg {
		e.trackObject(t, inc)
	}
}

func (e *Engine) trackObject(obj interface{}, inc int) {
	count, ok := e.refCount[obj]
	if !ok {
		count = 0
	}
	count += inc
	if count <= 0 {
		delete(e.refCount, obj)
	} else {
		e.refCount[obj] = count
	}
}
