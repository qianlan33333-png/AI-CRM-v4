package adapter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

func TestEnterpriseDirectoryUsesApplicationVisibleDepartmentReads(t *testing.T) {
	var listIDCalls int
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			if request.URL.Query().Get("corpsecret") != "application-secret" {
				t.Fatalf("unexpected credential path")
			}
			_, _ = response.Write([]byte(`{"errcode":0,"access_token":"application-token","expires_in":7200}`))
		case "/cgi-bin/agent/get":
			if request.URL.Query().Get("access_token") != "application-token" || request.URL.Query().Get("agentid") != "agent" {
				t.Fatalf("agent scope query=%s", request.URL.Query().Encode())
			}
			_, _ = response.Write([]byte(`{"errcode":0,"allow_userinfos":{"user":[]},"allow_partys":{"partyid":[1]},"allow_tags":null}`))
		case "/cgi-bin/department/simplelist":
			if request.URL.Query().Get("access_token") != "application-token" {
				t.Fatalf("department request did not use application token")
			}
			// The snapshot also contains an unrelated branch. allow_partys=[1]
			// must never cause a recursive read rooted at 9.
			_, _ = response.Write([]byte(`{"errcode":0,"department_id":[{"id":1,"parentid":0},{"id":2,"parentid":1},{"id":9,"parentid":0},{"id":10,"parentid":9}]}`))
		case "/cgi-bin/user/simplelist":
			query := request.URL.Query()
			if query.Get("department_id") != "1" || query.Get("fetch_child") != "1" {
				t.Fatalf("member query=%s", query.Encode())
			}
			// Deliberately reverse the Provider order: the adapter's local cursor
			// contract must not depend on user/simplelist response ordering.
			_, _ = response.Write([]byte(`{"errcode":0,"userlist":[{"userid":"Bob_02","name":"Bob"},{"userid":"Alice_01","name":"Alice"}]}`))
		case "/cgi-bin/user/list_id":
			listIDCalls++
			http.Error(response, "unexpected legacy endpoint", http.StatusInternalServerError)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	client, err := New(Config{Enabled: true, CorpID: "corp", AgentID: "agent", Secret: "application-secret", AdminCallbackURI: "https://crm.example/auth/wecom/callback", SidebarCallbackURI: "https://crm.example/api/sidebar/oauth/callback", APIBase: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	employees, err := client.ListEnterpriseEmployees(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if listIDCalls != 0 || len(employees) != 2 || employees[0].UserID != "Alice_01" || employees[1].DisplayName != "Bob" {
		t.Fatalf("list_id_calls=%d employees=%+v", listIDCalls, employees)
	}
	preflight := client.PreflightEnterpriseDirectory(context.Background())
	if !preflight.Complete || preflight.FailureStage != "" || preflight.AgentUserInfosShape != "object" || preflight.AgentPartysShape != "object" || preflight.AgentTagsShape != "null" || preflight.ScopeUsers != 0 || preflight.ScopeDepartments != 1 || preflight.DirectoryDepartments != 4 || preflight.DirectoryComponents != 1 || preflight.DepartmentEmployeeCount != 2 || preflight.DirectEmployeeCount != 0 || preflight.EmployeeCount != 2 {
		t.Fatalf("preflight=%+v", preflight)
	}
}

func TestEnterpriseDirectoryPreflightReportsSafeFailureStage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			_, _ = response.Write([]byte(`{"errcode":0,"access_token":"application-token","expires_in":7200}`))
		case "/cgi-bin/agent/get":
			_, _ = response.Write([]byte(`{"errcode":0,"allow_userinfos":{"user":[]},"allow_partys":{"partyid":[1]},"allow_tags":{"tagid":[7]}}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	client, err := New(Config{Enabled: true, CorpID: "corp", AgentID: "agent", Secret: "application-secret", AdminCallbackURI: "https://crm.example/auth/wecom/callback", SidebarCallbackURI: "https://crm.example/api/sidebar/oauth/callback", APIBase: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	preflight := client.PreflightEnterpriseDirectory(context.Background())
	if preflight.Complete || preflight.FailureStage != "agent_tags" || preflight.AgentUserInfosShape != "object" || preflight.AgentPartysShape != "object" || preflight.AgentTagsShape != "object" || preflight.AgentUserInfosDetail != "object_keys=1;user=array_len=0;element_types=none" || preflight.AgentPartysDetail != "object_keys=1;partyid=array_len=1;element_types=number" || preflight.AgentTagsDetail != "object_keys=1;tagid=array_len=1;element_types=number" || preflight.ScopeUsers != 0 || preflight.ScopeDepartments != 0 || preflight.EmployeeCount != 0 {
		t.Fatalf("preflight=%+v", preflight)
	}
	if got := (&Client{}).PreflightEnterpriseDirectory(context.Background()); got.Complete || got.FailureStage != "config" {
		t.Fatalf("config preflight=%+v", got)
	}
}

func TestEnterpriseDirectoryAllowsMissingOptionalTagsAfterCompleteEnumeration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			_, _ = response.Write([]byte(`{"errcode":0,"access_token":"application-token","expires_in":7200}`))
		case "/cgi-bin/agent/get":
			// Production's controlled readback has this shape: tags are omitted,
			// while user and department envelopes remain explicit objects.
			_, _ = response.Write([]byte(`{"errcode":0,"allow_userinfos":{"user":[]},"allow_partys":{"partyid":[1]}}`))
		case "/cgi-bin/department/simplelist":
			_, _ = response.Write([]byte(`{"errcode":0,"department_id":[{"id":1,"parentid":0}]}`))
		case "/cgi-bin/user/simplelist":
			if request.URL.Query().Get("department_id") != "1" || request.URL.Query().Get("fetch_child") != "1" {
				t.Fatalf("member query=%s", request.URL.Query().Encode())
			}
			_, _ = response.Write([]byte(`{"errcode":0,"userlist":[{"userid":"Visible_01","name":"Visible"}]}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	client, err := New(Config{Enabled: true, CorpID: "corp", AgentID: "agent", Secret: "application-secret", AdminCallbackURI: "https://crm.example/auth/wecom/callback", SidebarCallbackURI: "https://crm.example/api/sidebar/oauth/callback", APIBase: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	employees, err := client.ListEnterpriseEmployees(context.Background())
	if err != nil || len(employees) != 1 || employees[0].UserID != "Visible_01" {
		t.Fatalf("employees=%+v err=%v", employees, err)
	}
	preflight := client.PreflightEnterpriseDirectory(context.Background())
	if !preflight.Complete || preflight.FailureStage != "" || preflight.AgentUserInfosShape != "object" || preflight.AgentPartysShape != "object" || preflight.AgentTagsShape != "missing" || preflight.EmployeeCount != 1 {
		t.Fatalf("preflight=%+v", preflight)
	}
}

func TestEnterpriseDirectoryUnknownExactEmployeeIsDistinctFromUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			_, _ = response.Write([]byte(`{"errcode":0,"access_token":"application-token","expires_in":7200}`))
		case "/cgi-bin/user/get":
			if request.URL.Query().Get("userid") != "Alice" {
				t.Fatalf("userid=%q", request.URL.Query().Get("userid"))
			}
			_, _ = response.Write([]byte(`{"errcode":40003,"errmsg":"invalid user"}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	client, err := New(Config{Enabled: true, CorpID: "corp", AgentID: "agent", Secret: "application-secret", AdminCallbackURI: "https://crm.example/auth/wecom/callback", SidebarCallbackURI: "https://crm.example/api/sidebar/oauth/callback", APIBase: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ReadEnterpriseEmployee(context.Background(), "Alice")
	if !errors.Is(err, wecomport.ErrEnterpriseEmployeeNotFound) {
		t.Fatalf("error=%v", err)
	}
}

func TestEnterpriseDirectoryScopesCoverRootsAndIndependentVisibleBranches(t *testing.T) {
	scopes, err := enterpriseDirectoryScopes([]enterpriseDepartment{
		{id: 1, parentID: 0}, {id: 2, parentID: 1},
		{id: 9, parentID: 99}, {id: 10, parentID: 9},
	})
	if err != nil || len(scopes) != 2 || scopes[0] != 1 || scopes[1] != 9 {
		t.Fatalf("scopes=%v err=%v", scopes, err)
	}
}

func TestEnterpriseDirectoryScopesRejectCycle(t *testing.T) {
	if _, err := enterpriseDirectoryScopes([]enterpriseDepartment{{id: 1, parentID: 2}, {id: 2, parentID: 1}}); !errors.Is(err, ErrResponse) {
		t.Fatalf("error=%v", err)
	}
}

func TestEnterpriseDirectoryReadsEachVisibleComponentRecursively(t *testing.T) {
	requested := make(map[string]string)
	var requestedMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			_, _ = response.Write([]byte(`{"errcode":0,"access_token":"application-token","expires_in":7200}`))
		case "/cgi-bin/agent/get":
			_, _ = response.Write([]byte(`{"errcode":0,"allow_userinfos":{"user":[]},"allow_partys":{"partyid":[1,9]},"allow_tags":null}`))
		case "/cgi-bin/department/simplelist":
			_, _ = response.Write([]byte(`{"errcode":0,"department_id":[{"id":1,"parentid":0},{"id":2,"parentid":1},{"id":9,"parentid":99},{"id":10,"parentid":9}]}`))
		case "/cgi-bin/user/simplelist":
			departmentID := request.URL.Query().Get("department_id")
			requestedMu.Lock()
			requested[departmentID] = request.URL.Query().Get("fetch_child")
			requestedMu.Unlock()
			switch departmentID {
			case "1":
				_, _ = response.Write([]byte(`{"errcode":0,"userlist":[{"userid":"RootTree","name":"根部门"}]}`))
			case "9":
				_, _ = response.Write([]byte(`{"errcode":0,"userlist":[{"userid":"OrphanTree","name":"独立部门"}]}`))
			default:
				http.Error(response, "unexpected scope", http.StatusBadRequest)
			}
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	client, err := New(Config{Enabled: true, CorpID: "corp", AgentID: "agent", Secret: "application-secret", AdminCallbackURI: "https://crm.example/auth/wecom/callback", SidebarCallbackURI: "https://crm.example/api/sidebar/oauth/callback", APIBase: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	employees, err := client.ListEnterpriseEmployees(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	requestedMu.Lock()
	defer requestedMu.Unlock()
	if len(requested) != 2 || requested["1"] != "1" || requested["9"] != "1" || len(employees) != 2 || employees[0].UserID != "OrphanTree" || employees[1].UserID != "RootTree" {
		t.Fatalf("scopes=%v employees=%+v", requested, employees)
	}
}

func TestEnterpriseDirectoryMergesExplicitApplicationUsersOutsideDepartments(t *testing.T) {
	var directReads int
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			_, _ = response.Write([]byte(`{"errcode":0,"access_token":"application-token","expires_in":7200}`))
		case "/cgi-bin/agent/get":
			// agent/get returns objects; extra Provider metadata must not become a
			// second identity source or alter exact userid validation.
			_, _ = response.Write([]byte(`{"errcode":0,"allow_userinfos":{"user":[{"userid":"Direct_03","name":"untrusted provider metadata"}]},"allow_partys":{"partyid":[1]},"allow_tags":null}`))
		case "/cgi-bin/department/simplelist":
			_, _ = response.Write([]byte(`{"errcode":0,"department_id":[{"id":1,"parentid":0}]}`))
		case "/cgi-bin/user/simplelist":
			if request.URL.Query().Get("department_id") != "1" || request.URL.Query().Get("fetch_child") != "1" {
				t.Fatalf("department query=%s", request.URL.Query().Encode())
			}
			_, _ = response.Write([]byte(`{"errcode":0,"userlist":[{"userid":"Department_01","name":"部门成员"}]}`))
		case "/cgi-bin/user/get":
			if request.URL.Query().Get("userid") != "Direct_03" {
				t.Fatalf("direct userid=%q", request.URL.Query().Get("userid"))
			}
			directReads++
			_, _ = response.Write([]byte(`{"errcode":0,"userid":"Direct_03","name":"直接成员"}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	client, err := New(Config{Enabled: true, CorpID: "corp", AgentID: "agent", Secret: "application-secret", AdminCallbackURI: "https://crm.example/auth/wecom/callback", SidebarCallbackURI: "https://crm.example/api/sidebar/oauth/callback", APIBase: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	employees, err := client.ListEnterpriseEmployees(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if directReads != 1 || len(employees) != 2 || employees[0].UserID != "Department_01" || employees[1].UserID != "Direct_03" {
		t.Fatalf("direct_reads=%d employees=%+v", directReads, employees)
	}
}

func TestEnterpriseDirectoryRejectsTagOnlyOrIncompleteDepartmentScope(t *testing.T) {
	for name, agentResponse := range map[string]string{
		"nonempty_tags":        `{"errcode":0,"allow_userinfos":{"user":[]},"allow_partys":{"partyid":[1]},"allow_tags":{"tagid":[7]}}`,
		"invalid_nonnull_tags": `{"errcode":0,"allow_userinfos":{"user":[]},"allow_partys":{"partyid":[1]},"allow_tags":true}`,
		"missing_department":   `{"errcode":0,"allow_userinfos":{"user":[]},"allow_partys":{"partyid":[9]},"allow_tags":null}`,
		"unknown_scope_shape":  `{"errcode":0,"allow_userinfos":{"unexpected":[]},"allow_partys":{"partyid":[1]},"allow_tags":null}`,
		"invalid_user_member":  `{"errcode":0,"allow_userinfos":{"user":[{}]},"allow_partys":{"partyid":[1]},"allow_tags":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				response.Header().Set("Content-Type", "application/json")
				switch request.URL.Path {
				case "/cgi-bin/gettoken":
					_, _ = response.Write([]byte(`{"errcode":0,"access_token":"application-token","expires_in":7200}`))
				case "/cgi-bin/agent/get":
					_, _ = response.Write([]byte(agentResponse))
				case "/cgi-bin/department/simplelist":
					_, _ = response.Write([]byte(`{"errcode":0,"department_id":[{"id":1,"parentid":0}]}`))
				default:
					t.Fatalf("unexpected request %s", request.URL.Path)
				}
			}))
			defer server.Close()
			client, err := New(Config{Enabled: true, CorpID: "corp", AgentID: "agent", Secret: "application-secret", AdminCallbackURI: "https://crm.example/auth/wecom/callback", SidebarCallbackURI: "https://crm.example/api/sidebar/oauth/callback", APIBase: server.URL, HTTPClient: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.ListEnterpriseEmployees(context.Background()); err == nil {
				t.Fatal("expected incomplete directory error")
			}
		})
	}
}

func TestEnterpriseDirectoryAllowsEmptyScopeObjects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			_, _ = response.Write([]byte(`{"errcode":0,"access_token":"application-token","expires_in":7200}`))
		case "/cgi-bin/agent/get":
			_, _ = response.Write([]byte(`{"errcode":0,"allow_userinfos":{},"allow_partys":{}}`))
		case "/cgi-bin/department/simplelist":
			_, _ = response.Write([]byte(`{"errcode":0,"department_id":[]}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	client, err := New(Config{Enabled: true, CorpID: "corp", AgentID: "agent", Secret: "application-secret", AdminCallbackURI: "https://crm.example/auth/wecom/callback", SidebarCallbackURI: "https://crm.example/api/sidebar/oauth/callback", APIBase: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	employees, err := client.ListEnterpriseEmployees(context.Background())
	if err != nil || len(employees) != 0 {
		t.Fatalf("employees=%+v err=%v", employees, err)
	}
	preflight := client.PreflightEnterpriseDirectory(context.Background())
	if !preflight.Complete || preflight.FailureStage != "" || preflight.AgentUserInfosDetail != "object_keys=0;user=missing" || preflight.AgentPartysDetail != "object_keys=0;partyid=missing" || preflight.AgentTagsShape != "missing" || preflight.EmployeeCount != 0 {
		t.Fatalf("preflight=%+v", preflight)
	}
}

func TestEnterpriseDirectoryEnumeratesDirectUsersWithoutDepartmentReads(t *testing.T) {
	var departmentMemberReads int
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			_, _ = response.Write([]byte(`{"errcode":0,"access_token":"application-token","expires_in":7200}`))
		case "/cgi-bin/agent/get":
			_, _ = response.Write([]byte(`{"errcode":0,"allow_userinfos":{"user":[{"userid":"Direct_01"}]},"allow_partys":{}}`))
		case "/cgi-bin/department/simplelist":
			// A non-empty ungranted branch must not create a user/simplelist read.
			_, _ = response.Write([]byte(`{"errcode":0,"department_id":[{"id":9,"parentid":0}]}`))
		case "/cgi-bin/user/simplelist":
			departmentMemberReads++
			http.Error(response, "unexpected department member read", http.StatusInternalServerError)
		case "/cgi-bin/user/get":
			if request.URL.Query().Get("userid") != "Direct_01" {
				t.Fatalf("direct user query=%s", request.URL.Query().Encode())
			}
			_, _ = response.Write([]byte(`{"errcode":0,"userid":"Direct_01","name":"Direct"}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	client, err := New(Config{Enabled: true, CorpID: "corp", AgentID: "agent", Secret: "application-secret", AdminCallbackURI: "https://crm.example/auth/wecom/callback", SidebarCallbackURI: "https://crm.example/api/sidebar/oauth/callback", APIBase: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	employees, err := client.ListEnterpriseEmployees(context.Background())
	if err != nil || departmentMemberReads != 0 || len(employees) != 1 || employees[0].UserID != "Direct_01" {
		t.Fatalf("employees=%+v department_member_reads=%d err=%v", employees, departmentMemberReads, err)
	}
	preflight := client.PreflightEnterpriseDirectory(context.Background())
	if !preflight.Complete || preflight.FailureStage != "" || preflight.DirectoryComponents != 0 || preflight.DepartmentEmployeeCount != 0 || preflight.DirectEmployeeCount != 1 || preflight.EmployeeCount != 1 {
		t.Fatalf("preflight=%+v", preflight)
	}
}
