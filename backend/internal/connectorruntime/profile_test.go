package connectorruntime

import (
	"github.com/user/dbhub/internal/connector"
	"github.com/user/dbhub/internal/connector/native"
	"github.com/user/dbhub/internal/db"
	"testing"
)

func TestCustomNetworkTemplateDoesNotRequireProductBranch(t *testing.T) {
	template := db.DatasourceTemplate{Key: "custom-oracle", Name: "自定义服务", Product: "Oracle", Enabled: true, URLTemplate: "jdbc:oracle:thin:@//{host}:{port}/{service_name}", Driver: db.JDBCDriver{Enabled: true, ArtifactID: "custom-driver-1", SHA256: "expected-pin", ClassName: "oracle.jdbc.OracleDriver"}, Fields: []db.TemplateField{{Name: "host", Label: "主机", Type: "string", Required: true}, {Name: "port", Label: "端口", Type: "integer", Required: true}, {Name: "service_name", Label: "服务", Type: "string", Required: true}}}
	source := db.Connection{Type: "jdbc", Template: &template, Database: "finance", SSLMode: "disable", Parameters: map[string]string{"host": "db.example.test", "port": "1521", "service_name": "finance"}}
	spec, e := BuildJDBCSpec(&source)
	if e != nil {
		t.Fatal(e)
	}
	if spec.URL != "jdbc:oracle:thin:@//db.example.test:1521/finance" || spec.SHA256 != "expected-pin" {
		t.Fatal(spec)
	}
	caps := source.SupportedCapabilities()
	if !caps.Metadata || caps.SQL || caps.Collect || caps.Transactions {
		t.Fatal("unverified product inherited unsafe capabilities", caps)
	}
	template.Dialect = "postgres"
	if e = ValidateTemplate(&template); e == nil {
		t.Fatal("unimplemented dialect accepted")
	}
	template.Dialect = ""
	template.URLTemplate += "?password=literal"
	if e = ValidateTemplate(&template); e == nil {
		t.Fatal("URL credentials accepted")
	}
}
func TestTemplateRejectsInjectedParametersAndUnknownConnectors(t *testing.T) {
	template := db.DatasourceTemplate{Key: "pg", Name: "PG", Product: "PostgreSQL", Driver: db.JDBCDriver{ClassName: "org.postgresql.Driver"}, URLTemplate: "jdbc:postgresql://{host}:{port}/{database}", Fields: []db.TemplateField{{Name: "host", Label: "主机", Type: "string", Required: true}, {Name: "port", Label: "端口", Type: "integer", Required: true}, {Name: "database", Label: "数据库", Type: "string", Required: true}}}
	for _, params := range []map[string]string{{"host": "db", "port": "5432", "database": "x?password=leak"}, {"host": "db", "port": "65536", "database": "x"}, {"host": "db", "port": "5432", "database": "x", "password": "leak"}} {
		if _, e := NormalizeParameters(&template, params); e == nil {
			t.Fatal("invalid parameter set accepted")
		}
	}
	registry := connector.NewRegistry()
	if e := registry.Register(native.Driver{Kind: "mysql"}); e != nil {
		t.Fatal(e)
	}
	if e := registry.Register(native.Driver{Kind: "mysql"}); e == nil {
		t.Fatal("duplicate registry entry")
	}
	if _, e := registry.Resolve("unknown"); e == nil {
		t.Fatal("unknown connector fell back")
	}
}
