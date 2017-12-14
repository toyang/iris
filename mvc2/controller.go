package mvc2

import (
	"fmt"
	"reflect"

	"github.com/kataras/di"

	"github.com/kataras/iris/context"
	"github.com/kataras/iris/core/router"
	"github.com/kataras/iris/core/router/macro"
)

// BaseController is the controller interface,
// which the main request `C` will implement automatically.
// End-dev doesn't need to have any knowledge of this if she/he doesn't want to implement
// a new Controller type.
// Controller looks the whole flow as one handler, so `ctx.Next`
// inside `BeginRequest` is not be respected.
// Alternative way to check if a middleware was procceed successfully
// and called its `ctx.Next` is the `ctx.Proceed(handler) bool`.
// You have to navigate to the `context/context#Proceed` function's documentation.
type BaseController interface {
	BeginRequest(context.Context)
	EndRequest(context.Context)
}

// C is the basic BaseController type that can be used as an embedded anonymous field
// to custom end-dev controllers.
//
// func(c *ExampleController) Get() string |
// (string, string) |
// (string, int) |
// int |
// (int, string |
// (string, error) |
// bool |
// (any, bool) |
// error |
// (int, error) |
// (customStruct, error) |
// customStruct |
// (customStruct, int) |
// (customStruct, string) |
// Result or (Result, error)
// where Get is an HTTP Method func.
//
// Look `core/router#APIBuilder#Controller` method too.
//
// It completes the `activator.BaseController` interface.
//
// Example at: https://github.com/kataras/iris/tree/master/_examples/mvc/overview/web/controllers.
// Example usage at: https://github.com/kataras/iris/blob/master/mvc/method_result_test.go#L17.
type C struct {
	// The current context.Context.
	//
	// we have to name it for two reasons:
	// 1: can't ignore these via reflection, it doesn't give an option to
	// see if the functions is derived from another type.
	// 2: end-developer may want to use some method functions
	// or any fields that could be conflict with the context's.
	Ctx context.Context
}

var _ BaseController = &C{}

// BeginRequest does nothing anymore, is here to complet ethe `BaseController` interface.
// BaseController is not required anymore, `Ctx` is binded automatically by the engine's
// wrapped Handler.
func (c *C) BeginRequest(ctx context.Context) {}

// EndRequest does nothing, is here to complete the `BaseController` interface.
func (c *C) EndRequest(ctx context.Context) {}

// ControllerActivator returns a new controller type info description.
// Its functionality can be overriden by the end-dev.
type ControllerActivator struct {
	// the router is used on the `Activate` and can be used by end-dev on the `OnActivate`
	// to register any custom controller's functions as handlers but we will need it here
	// in order to not create a new type like `ActivationPayload` for the `OnActivate`.
	Router router.Party

	// initRef BaseController // the BaseController as it's passed from the end-dev.
	Value reflect.Value // the BaseController's Value.
	Type  reflect.Type  // raw type of the BaseController (initRef).
	// FullName it's the last package path segment + "." + the Name.
	// i.e: if login-example/user/controller.go, the FullName is "user.Controller".
	FullName string

	// the methods names that is already binded to a handler,
	// the BeginRequest, EndRequest and OnActivate are reserved by the internal implementation.
	reservedMethods []string

	// the bindings that comes from the Engine and the controller's filled fields if any.
	// Can be binded to the the new controller's fields and method that is fired
	// on incoming requests.
	Dependencies *di.D

	// on activate.
	injector *di.StructInjector
}

func newControllerActivator(router router.Party, controller interface{}, d *di.D) *ControllerActivator {
	var (
		val = reflect.ValueOf(controller)
		typ = val.Type()

		// the full name of the controller, it's its type including the package path.
		fullName = getNameOf(typ)
	)

	// the following will make sure that if
	// the controller's has set-ed pointer struct fields by the end-dev
	// we will include them to the bindings.
	// set bindings to the non-zero pointer fields' values that may be set-ed by
	// the end-developer when declaring the controller,
	// activate listeners needs them in order to know if something set-ed already or not,
	// look `BindTypeExists`.
	d.Values = append(lookupNonZeroFieldsValues(val), d.Values...)

	c := &ControllerActivator{
		// give access to the Router to the end-devs if they need it for some reason,
		// i.e register done handlers.
		Router:   router,
		Value:    val,
		Type:     typ,
		FullName: fullName,
		// set some methods that end-dev cann't use accidentally
		// to register a route via the `Handle`,
		// all available exported and compatible methods
		// are being appended to the slice at the `parseMethods`,
		// if a new method is registered via `Handle` its function name
		// is also appended to that slice.
		//
		// TODO: now that BaseController is totally optionally
		// we have to check if BeginRequest and EndRequest should be here.
		reservedMethods: whatReservedMethods(typ),
		Dependencies:    d,
	}

	return c
}

func whatReservedMethods(typ reflect.Type) []string {
	methods := []string{"OnActivate"}
	if isBaseController(typ) {
		methods = append(methods, "BeginRequest", "EndRequest")
	}

	return methods
}

// checks if a method is already registered.
func (c *ControllerActivator) isReservedMethod(name string) bool {
	for _, s := range c.reservedMethods {
		if s == name {
			return true
		}
	}

	return false
}

// register all available, exported methods to handlers if possible.
func (c *ControllerActivator) parseMethods() {
	n := c.Type.NumMethod()
	for i := 0; i < n; i++ {
		m := c.Type.Method(i)

		httpMethod, httpPath, err := parseMethod(m, c.isReservedMethod)
		if err != nil {
			if err != errSkip {
				err = fmt.Errorf("MVC: fail to parse the route path and HTTP method for '%s.%s': %v", c.FullName, m.Name, err)
				c.Router.GetReporter().AddErr(err)

			}
			continue
		}

		c.Handle(httpMethod, httpPath, m.Name)
	}
}

func (c *ControllerActivator) activate() {
	c.injector = c.Dependencies.Struct(c.Value)
	c.parseMethods()
}

var emptyIn = []reflect.Value{}

// Handle registers a route based on a http method, the route's path
// and a function name that belongs to the controller, it accepts
// a forth, optionally, variadic parameter which is the before handlers.
//
// Just like `APIBuilder`, it returns the `*router.Route`, if failed
// then it logs the errors and it returns nil, you can check the errors
// programmatically by the `APIBuilder#GetReporter`.
func (c *ControllerActivator) Handle(method, path, funcName string, middleware ...context.Handler) *router.Route {
	if method == "" || path == "" || funcName == "" ||
		c.isReservedMethod(funcName) {
		// isReservedMethod -> if it's already registered
		// by a previous Handle or analyze methods internally.
		return nil
	}

	// get the method from the controller type.
	m, ok := c.Type.MethodByName(funcName)
	if !ok {
		err := fmt.Errorf("MVC: function '%s' doesn't exist inside the '%s' controller",
			funcName, c.FullName)
		c.Router.GetReporter().AddErr(err)
		return nil
	}

	// parse a route template which contains the parameters organised.
	tmpl, err := macro.Parse(path, c.Router.Macros())
	if err != nil {
		err = fmt.Errorf("MVC: fail to parse the path for '%s.%s': %v", c.FullName, funcName, err)
		c.Router.GetReporter().AddErr(err)
		return nil
	}

	// add this as a reserved method name in order to
	// be sure that the same func will not be registered again, even if a custom .Handle later on.
	c.reservedMethods = append(c.reservedMethods, funcName)

	// get the function's input.
	funcIn := getInputArgsFromFunc(m.Type)

	// get the path parameters bindings from the template,
	// use the function's input except the receiver which is the
	// end-dev's controller pointer.
	pathParams := getPathParamsForInput(tmpl.Params, funcIn[1:]...)
	// get the function's input arguments' bindings.
	funcDependencies := c.Dependencies.Clone()
	funcDependencies.Add(pathParams...)
	funcInjector := funcDependencies.Func(m.Func)

	// we will make use of 'n' to make a slice of reflect.Value
	// to pass into if the function has input arguments that
	// are will being filled by the funcDependencies.
	n := len(funcIn)
	// the element value, not the pointer, wil lbe used to create a
	// new controller on each incoming request.
	elemTyp := indirectTyp(c.Type)

	implementsBase := isBaseController(c.Type)

	handler := func(ctx context.Context) {
		ctrl := reflect.New(elemTyp)

		if implementsBase {
			// the Interface(). is faster than MethodByName or pre-selected methods.
			b := ctrl.Interface().(BaseController)
			// init the request.
			b.BeginRequest(ctx)

			// if begin request stopped the execution.
			if ctx.IsStopped() {
				return
			}

			defer b.EndRequest(ctx)
		}

		if !c.injector.Valid && !funcInjector.Valid {
			DispatchFuncResult(ctx, ctrl.Method(m.Index).Call(emptyIn))
		} else {
			ctxValue := reflect.ValueOf(ctx)
			if c.injector.Valid {
				elem := ctrl.Elem()
				c.injector.InjectElem(elem, ctxValue)
				if ctx.IsStopped() {
					return
				}

				// we do this in order to reduce in := make...
				// if not func input binders, we execute the handler with empty input args.
				if !funcInjector.Valid {
					DispatchFuncResult(ctx, ctrl.Method(m.Index).Call(emptyIn))
				}
			}
			// otherwise, it has one or more valid input binders,
			// make the input and call the func using those.
			if funcInjector.Valid {
				in := make([]reflect.Value, n, n)
				in[0] = ctrl
				funcInjector.Inject(&in, ctxValue)
				if ctx.IsStopped() {
					return
				}

				DispatchFuncResult(ctx, m.Func.Call(in))
			}

		}
	}

	// register the handler now.
	route := c.Router.Handle(method, path, append(middleware, handler)...)
	if route != nil {
		// change the main handler's name in order to respect the controller's and give
		// a proper debug message.
		route.MainHandlerName = fmt.Sprintf("%s.%s", c.FullName, funcName)
	}

	return route
}