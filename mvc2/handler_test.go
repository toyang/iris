package mvc2_test

// black-box

import (
	"fmt"
	"testing"

	"github.com/kataras/iris"
	"github.com/kataras/iris/httptest"
	. "github.com/kataras/iris/mvc2"
)

// dynamic func
type testUserStruct struct {
	ID       int64
	Username string
}

func testBinderFunc(ctx iris.Context) testUserStruct {
	id, _ := ctx.Params().GetInt64("id")
	username := ctx.Params().Get("username")
	return testUserStruct{
		ID:       id,
		Username: username,
	}
}

// service
type (
	testService interface {
		Say(string) string
	}
	testServiceImpl struct {
		prefix string
	}
)

func (s *testServiceImpl) Say(message string) string {
	return s.prefix + " " + message
}

var (
	// binders, as user-defined
	testBinderFuncUserStruct = testBinderFunc
	testBinderService        = &testServiceImpl{prefix: "say"}
	testBinderFuncParam      = func(ctx iris.Context) string {
		return ctx.Params().Get("param")
	}

	// consumers
	// a context as first input arg, which is not needed to be binded manually,
	// and a user struct which is binded to the input arg by the #1 func(ctx) any binder.
	testConsumeUserHandler = func(ctx iris.Context, user testUserStruct) {
		ctx.JSON(user)
	}

	// just one input arg, the service which is binded by the #2 service binder.
	testConsumeServiceHandler = func(service testService) string {
		return service.Say("something")
	}
	// just one input arg, a standar string which is binded by the #3 func(ctx) any binder.
	testConsumeParamHandler = func(myParam string) string {
		return "param is: " + myParam
	}
)

func TestMakeHandler(t *testing.T) {
	binders := []*InputBinder{
		// #1
		MustMakeFuncInputBinder(testBinderFuncUserStruct),
		// #2
		MustMakeServiceInputBinder(testBinderService),
		// #3
		MustMakeFuncInputBinder(testBinderFuncParam),
	}

	var (
		h1 = MustMakeHandler(testConsumeUserHandler, binders)
		h2 = MustMakeHandler(testConsumeServiceHandler, binders)
		h3 = MustMakeHandler(testConsumeParamHandler, binders)
	)

	testAppWithMvcHandlers(t, h1, h2, h3)
}

func testAppWithMvcHandlers(t *testing.T, h1, h2, h3 iris.Handler) {
	app := iris.New()
	app.Get("/{id:long}/{username:string}", h1)
	app.Get("/service", h2)
	app.Get("/param/{param:string}", h3)

	expectedUser := testUserStruct{
		ID:       42,
		Username: "kataras",
	}

	e := httptest.New(t, app)
	// 1
	e.GET(fmt.Sprintf("/%d/%s", expectedUser.ID, expectedUser.Username)).Expect().Status(httptest.StatusOK).
		JSON().Equal(expectedUser)
	// 2
	e.GET("/service").Expect().Status(httptest.StatusOK).
		Body().Equal("say something")
	// 3
	e.GET("/param/the_param_value").Expect().Status(httptest.StatusOK).
		Body().Equal("param is: the_param_value")
}