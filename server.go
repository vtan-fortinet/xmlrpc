package xmlrpc

import (
	"os"
	"io"
	"fmt"
	"bytes"
	//"io/ioutil"
    "runtime"
	"reflect"
	"strings"
	"net/http"
	"encoding/xml"
)

type methodData struct {
	obj       interface{}
	//method    reflect.Method
    ftype       reflect.Type    // function/method type
    fvalue      reflect.Value   // function/method value
	padParams bool
}

// Map from XML-RPC procedure names to Go methods
type Handler struct {
	methods map[string]*methodData
}

// create a new handler mapping XML-RPC procedure names to Go methods
func NewHandler() *Handler {
	h := new(Handler)
	h.methods = make(map[string]*methodData)
	return h
}

// register all methods associated with the Go object, passing them
// through the name mapper if one is supplied
//
// The name mapper can return "" to ignore a method or transform the
// name as desired
func (h *Handler) Register(obj interface{}, mapper func(string) string,
	padParams bool) error {
	ot := reflect.TypeOf(obj)

	for i := 0; i < ot.NumMethod(); i++ {
		m := ot.Method(i)
		if m.PkgPath != "" {
			continue
		}

		var name string
		if mapper == nil {
			name = m.Name
		} else {
			name = mapper(m.Name)
			if name == "" {
				continue
			}
		}

		md := &methodData{obj: obj, ftype: m.Type, fvalue: m.Func, padParams: padParams}
		h.methods[name] = md
		h.methods[strings.ToLower(name)] = md
	}

	return nil
}


// register a func, if name is "", then use func name
func (h *Handler) RegFunc(obj interface{}, name string, padParams bool) error {
	vo := reflect.ValueOf(obj)
    if vo.Kind() != reflect.Func {
        panic("RegFunc only register function")
    }
    md := &methodData{obj: nil, ftype: vo.Type(), fvalue: vo, padParams: padParams}
    if name == "" {
        // runtime.FuncForPC always return pkg.func_name, so we cut prefix "main."
        name = runtime.FuncForPC(vo.Pointer()).Name()[5:]
    }
    h.methods[name] = md
    return nil
}


var faultType = reflect.TypeOf((*Fault)(nil))

// Return an XML-RPC fault
func writeFault(out io.Writer, code int, msg string) {
	fmt.Fprintf(out, `<?xml version="1.0"?>
<methodResponse>
  <fault>
	<value>
		<struct>
		  <member>
			<name>faultCode</name>
			<value><int>%d</int></value>
		  </member>
		  <member>
			<name>faultString</name>
			<value>`, code)
	err := xml.EscapeText(out, []byte(msg))
	fmt.Fprintf(out, `</value>
		  </member>
		</struct>
	</value>
  </fault>
</methodResponse>`)

	// XXX dump the error to Stderr for now
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot write fault#%d(%s): %v\n", code, msg,
			err)
	}
}

// semi-standard XML-RPC response codes
const (
	errNotWellFormed = -32700
	errUnknownMethod = -32601
	errInvalidParams = -32602
	errInternal      = -32603
)

// handle an XML-RPC request
func (h *Handler) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
  //b, _ := ioutil.ReadAll(req.Body)
  //body := string(b)
  //fmt.Fprintf(os.Stderr, body)
  //methodName, params, err, fault := UnmarshalString(body)
  //fmt.Fprintf(os.Stderr, "ServeHTTP params = %v\n", params)

	methodName, params, err, fault := Unmarshal(req.Body)

	if err != nil {
		writeFault(resp, errNotWellFormed,
			fmt.Sprintf("Unmarshal error: %v", err))
		return
	} else if fault != nil {
		writeFault(resp, fault.Code, fault.Msg)
		return
	}

	var args []interface{}
	var ok bool

	if args, ok = params.([]interface{}); !ok {
		args := make([]interface{}, 1, 1)
		args[0] = params
	}
  //fmt.Fprintf(os.Stderr, "%v", args)

	var mData *methodData

	if mData, ok = h.methods[methodName]; !ok {
		writeFault(resp, errUnknownMethod,
			fmt.Sprintf("Unknown method \"%s\"", methodName))
		return
	}

	expArgs := mData.ftype.NumIn()
    x := 0
    if mData.obj != nil { x = 1 }
	if len(args) + x != expArgs {
		if !mData.padParams || len(args) + x > expArgs {
			writeFault(resp, errInvalidParams,
				fmt.Sprintf("Bad number of parameters for method \"%s\","+
					" (%d != %d)", methodName, len(args), expArgs-1))
			return
		}
	}

	vals := make([]reflect.Value, expArgs, expArgs)

    i := x
    if x == 1 {
        vals[0] = reflect.ValueOf(mData.obj)
    }
	for ; i < expArgs; i++ {
		if mData.padParams && i >= len(args) {
			vals[i] = reflect.Zero(mData.ftype.In(i))
			continue
		}

		if !reflect.TypeOf(args[i-x]).ConvertibleTo(mData.ftype.In(i)) {
			writeFault(resp, errInvalidParams,
				fmt.Sprintf("Bad %s argument #%d (%v should be %v)",
					methodName, i-x, reflect.TypeOf(args[i-x]),
					mData.ftype.In(i)))
			return
		}

		vals[i] = reflect.ValueOf(args[i-x])
	}

	rtnVals := mData.fvalue.Call(vals)

	if len(rtnVals) == 1 && reflect.TypeOf(rtnVals[0].Interface()) == faultType {
		if fault, ok := rtnVals[0].Interface().(*Fault); ok {
			writeFault(resp, fault.Code, fault.Msg)
			return
		}
	}

	mArray := make([]interface{}, len(rtnVals), len(rtnVals))
	for i := 0; i < len(rtnVals); i++ {
		mArray[i] = rtnVals[i].Interface()
	}

	buf := bytes.NewBufferString("")
	err = marshalArray(buf, "", mArray)
	if err != nil {
		writeFault(resp, errInternal, fmt.Sprintf("Failed to marshal %s: %v",
			methodName, err))
		return
	}
  fmt.Fprintf(os.Stderr, buf.String())
	buf.WriteTo(resp)
}

// start an XML-RPC server
/*
func StartServer(port int) *Handler {
	h := NewHandler()
	http.HandleFunc("/", h.HandleRequest)
	go http.ListenAndServe(fmt.Sprintf(":%d", port), nil)
	return h
}
*/
