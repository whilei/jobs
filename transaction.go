package zazu

import (
	"fmt"
	"github.com/garyburd/redigo/redis"
)

type transaction struct {
	conn    redis.Conn
	actions []*action
}

type action struct {
	kind    actionKind
	name    string
	script  *redis.Script
	args    redis.Args
	handler replyHandler
}

type actionKind int

const (
	actionCommand = iota
	actionScript
)

type replyHandler func(interface{}) error

func newTransaction() *transaction {
	t := &transaction{
		conn: redisPool.Get(),
	}
	return t
}

func (t *transaction) command(name string, args redis.Args, handler replyHandler) {
	t.actions = append(t.actions, &action{
		kind:    actionCommand,
		name:    name,
		args:    args,
		handler: handler,
	})
}

func (t *transaction) script(script *redis.Script, args redis.Args, handler replyHandler) {
	t.actions = append(t.actions, &action{
		kind:    actionScript,
		script:  script,
		args:    args,
		handler: handler,
	})
}

func (t *transaction) sendAction(a *action) error {
	switch a.kind {
	case actionCommand:
		return t.conn.Send(a.name, a.args...)
	case actionScript:
		return a.script.Send(t.conn, a.args...)
	}
	return nil
}

func (t *transaction) doAction(a *action) (interface{}, error) {
	switch a.kind {
	case actionCommand:
		return t.conn.Do(a.name, a.args...)
	case actionScript:
		return a.script.Do(t.conn, a.args...)
	}
	return nil, nil
}

func (t *transaction) exec() error {
	// Return the connection to the pool when we are done
	defer t.conn.Close()

	if len(t.actions) == 1 {
		// If there is only one command, no need to use MULTI/EXEC
		a := t.actions[0]
		reply, err := t.doAction(a)
		if err != nil {
			return err
		}
		if a.handler != nil {
			if err := a.handler(reply); err != nil {
				return err
			}
		}
	} else {
		// Send all the commands and scripts at once using MULTI/EXEC
		t.conn.Send("MULTI")
		for _, a := range t.actions {
			if err := t.sendAction(a); err != nil {
				return err
			}
		}

		// Invoke redis driver to execute the transaction
		replies, err := redis.Values(t.conn.Do("EXEC"))
		if err != nil {
			return err
		}

		// Iterate through the replies, calling the corresponding handler functions
		for i, reply := range replies {
			a := t.actions[i]
			if a.handler != nil {
				if err := a.handler(reply); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// newScanJobHandler returns a replyHandler which, when run, will scan the values
// of reply into job.
func newScanJobHandler(job *Job) replyHandler {
	return func(reply interface{}) error {
		return scanJob(reply, job)
	}
}

// newScanStringsHandler returns a replyHandler which, when run, will scan the values
// of reply into strings.
func newScanStringsHandler(strings *[]string) replyHandler {
	return func(reply interface{}) error {
		if strings == nil {
			return fmt.Errorf("zazu: Error in scanStringsHandler: expected strings arg to be a pointer to a slice of strings but it was nil")
		}
		var err error
		(*strings), err = redis.Strings(reply, nil)
		if err != nil {
			return fmt.Errorf("zazu: Error in scanStringsHandler: %s", err.Error())
		}
		return nil
	}
}
