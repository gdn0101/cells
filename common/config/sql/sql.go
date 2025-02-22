package sql

import (
	"bytes"
	"errors"
	"github.com/pydio/cells/common/dao"
	"github.com/pydio/cells/common/sql"
	"github.com/pydio/cells/x/configx"
	json "github.com/pydio/cells/x/jsonx"
	"github.com/pydio/packr"
	migrate "github.com/rubenv/sql-migrate"
)

type SQL struct {
	dao    dao.DAO
	config configx.Values
	watchers []*receiver
}

func New(driver string, dsn string, prefix string) configx.Entrypoint {
	var d dao.DAO
	switch driver {
	case "mysql":
		if c := sql.NewDAO(driver, dsn, prefix); c != nil {
			d = NewDAO(c)
		}
	case "sqlite3":
		if c := sql.NewDAO(driver, dsn, prefix); c != nil {
			d = NewDAO(c)
		}
	}

	dc := configx.New()
	dc.Val("prepare").Set(true)

	d.Init(dc)

	return &SQL{
		dao: d,
	}
}

// Init handler for the SQL DAO
func (s *SQL) Init(options configx.Values) error {

	migrations := &sql.PackrMigrationSource{
		Box:         packr.NewBox("../../../common/config/sql/migrations"),
		Dir:         "./" + s.dao.Driver(),
		TablePrefix: s.dao.Prefix(),
	}

	sqldao := s.dao.(sql.DAO)

	_, err := sql.ExecMigration(sqldao.DB(), s.dao.Driver(), migrations, migrate.Up, s.dao.Prefix())
	if err != nil {
		return err
	}

	// Preparing the db statements
	if options.Val("prepare").Default(true).Bool() {
		for key, query := range queries {
			if err := sqldao.Prepare(key, query); err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *SQL) Val(path ...string) configx.Values {
	if s.config == nil {
		s.Get()
	}
	return &wrappedConfig{s.config.Val(path...), s}
}

func (s *SQL) Get() configx.Value {
	dao := s.dao.(DAO)

	v := configx.New(configx.WithJSON())

	b, err := dao.Get()
	if err != nil {
		v.Set(map[string]interface{}{})
	}

	v.Set(b)

	s.config = v

	return v
}

func (s *SQL) Set(data interface{}) error {
	dao := s.dao.(DAO)

	b, err := json.Marshal(data)
	if err != nil {
		return err
	}

	if err := dao.Set(b); err != nil {
		return err
	}

	v := configx.New(configx.WithJSON())
	v.Set(b)

	s.config = v

	s.update()

	return nil
}

func (s *SQL) update() {
	for _, w := range s.watchers {
		v := s.Val(w.path...).Bytes()
		select {
		case w.updates <- v:
			default:
		}
	}
}

func (s *SQL) Del() error {
	return s.Set(nil)
}

func (s *SQL) Save(ctxUser, ctxMessage string) error {
	return nil
}

func (s *SQL) Watch(path ...string) (configx.Receiver, error) {
	r := &receiver{
		exit: make(chan bool),
		path: path,
		value: s.Val(path...).Bytes(),
		updates: make(chan []byte),
	}

	s.watchers = append(s.watchers, r)

	return r, nil
}

type receiver struct {
	exit chan bool
	path []string
	value []byte
	updates chan []byte
}

func (r *receiver) Next() (configx.Values, error) {
	for {
		select {
		case <-r.exit:
			return nil, errors.New("watcher stopped")
		case v := <-r.updates:
			if len(r.value) == 0 && len(v) == 0 {
				continue
			}

			if bytes.Equal(r.value, v) {
				continue
			}

			r.value = v

			ret := configx.New(configx.WithJSON())
			if err := ret.Set(v); err != nil {
				return nil, err
			}
			return ret, nil
		}
	}
}

func (r *receiver) Stop() {
	select {
	case <-r.exit:
	default:
		close(r.exit)
	}
	return
}

type wrappedConfig struct {
	configx.Values
	s *SQL
}

func (w *wrappedConfig) Set(val interface{}) error {
	err := w.Values.Set(val)
	if err != nil {
		return err
	}

	return w.s.Set(w.Values.Val("#").Map())
}
