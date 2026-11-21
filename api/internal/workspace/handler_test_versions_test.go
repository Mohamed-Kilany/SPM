package workspace

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func sharedStepTestContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader("{}"))
	return c
}

func TestValidateSharedStepRequestTrimsAndAccepts(t *testing.T) {
	req := sharedStepRequest{
		Name:           "  Sign in  ",
		Action:         "  Open the sign-in page  ",
		ExpectedResult: "  Dashboard is shown  ",
	}
	if !validateSharedStepRequest(sharedStepTestContext(), &req) {
		t.Fatal("valid shared step was rejected")
	}
	if req.Name != "Sign in" || req.Action != "Open the sign-in page" || req.ExpectedResult != "Dashboard is shown" {
		t.Fatalf("request was not normalized: %+v", req)
	}
}

func TestValidateSharedStepRequestRejectsMissingFields(t *testing.T) {
	cases := []sharedStepRequest{
		{Action: "Do", ExpectedResult: "See"},
		{Name: "Name", ExpectedResult: "See"},
		{Name: "Name", Action: "Do"},
		{Name: strings.Repeat("x", 256), Action: "Do", ExpectedResult: "See"},
	}
	for _, req := range cases {
		req := req
		t.Run(req.Name, func(t *testing.T) {
			if validateSharedStepRequest(sharedStepTestContext(), &req) {
				t.Fatalf("invalid shared step was accepted: %+v", req)
			}
		})
	}
}
