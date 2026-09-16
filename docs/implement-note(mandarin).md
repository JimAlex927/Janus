

## 1、go中的Transport是什么



Go 官方把 `Transport` 定义为 `RoundTripper` 的实现，并明确说它是 HTTP/HTTPS 请求的一个**低层客户端原语**，同时负责连接缓存和复用；它应该长期复用，而不是每个请求创建一个。



```go
client := &http.Client{
    Transport: transport,
}

resp, err := client.Do(req)
```

上述代码的原理是：

```
http.Client
    │
    │ 高层 HTTP Client
    │
    ├── redirect
    ├── cookie
    ├── timeout
    │
    ▼
http.Transport
    │
    │ 真正负责把这个 HTTP request 运出去
    │
    ├── 找现成连接
    ├── 没有连接 -> DNS/TCP connect
    ├── HTTPS -> TLS handshake
    ├── 写 HTTP request
    ├── 等 HTTP response header
    ├── 返回 Response
    └── Response.Body 读取结束后连接可能回连接池
```

所以虽然叫 `Transport`，它并不只是 TCP 层。所以是网络的传输层，计算机网络里面，传输层协议一般只有TCP和UDP。

所以它是传输层，但和TCP、UDP也不完全一样，就是你可以理解为大体上也还是http tansport，但也可以部分修改tcp的一些参数之类的。不要想着通过它来完全管理tcp和udp。

**HTTP Client 的连接管理 + HTTP 传输层实现。**

它横跨了：

```
HTTP
TLS
TCP
连接池
```



## 2、然后当前代码中创建transport用到的配置：

```
internal/proxy/proxy.go:21


func NewTransport(values ...config.BackendSettings) *http.Transport {
	settings := config.DefaultSettings().Backend
	if len(values) > 0 {
		settings = values[0]
	}
	settings = settings.WithDefaults()
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil // Backend connections must not inherit a workstation's HTTP_PROXY.
	t.DialContext = (&net.Dialer{Timeout: settings.ConnectTimeout.Duration(), KeepAlive: settings.KeepAlive.Duration()}).DialContext
	//Mandarin: 下面是Transport的配置. Transport是请求已经抵达gateway了。然后反向代理的时候，gateway需要请求service.
	//也就是一个网络传输层.
	//Go 官方把 Transport 定义为 RoundTripper 的实现，并明确说它是 HTTP/HTTPS 请求的一个低层客户端原语，同时负责连接缓存和复用；
	//它应该长期复用，而不是每个请求创建一个:
	//Clients and Transports are safe for concurrent use by multiple goroutines and for efficiency should only be created once and re-used.
	//config - 1
	t.TLSHandshakeTimeout = settings.TLSHandshakeTimeout.Duration()
	t.ResponseHeaderTimeout = settings.ResponseHeaderTimeout.Duration()
	t.MaxResponseHeaderBytes = settings.MaxResponseHeaderBytes
	t.MaxIdleConns = settings.MaxIdleConns
	t.MaxIdleConnsPerHost = settings.MaxIdleConnsPerHost
	t.MaxConnsPerHost = settings.MaxConnsPerHost
	t.IdleConnTimeout = settings.IdleConnTimeout.Duration()
	t.DisableCompression = settings.DisableCompression != nil && *settings.DisableCompression
	return t
}

```



配置分为四类：

```
① 建连相关
ConnectTimeout
KeepAlive

② HTTPS 相关
TLSHandshakeTimeout

③ HTTP Response 相关
ResponseHeaderTimeout
MaxResponseHeaderBytes

④ 连接池相关
MaxIdleConns
MaxIdleConnsPerHost
MaxConnsPerHost
IdleConnTimeout

其它配置：
Proxy
DisableCompression
```

为了讲解这些配置，我们先要搞懂一次请求，用transport，经历了什么？

```

假设请求
GET https://backend:8443/api/user
```

```
Transport 大概经历：
                         一次 backend HTTP request

① 从连接池找连接
        │
        ├── 找到了 ─────────────────────┐
        │                              │
        └── 没找到                     │
             │                         │
             ▼                         │
② DNS + TCP connect                    │
   ← ConnectTimeout →                  │
             │                         │
             ▼                         │
③ TLS handshake                       │
   ← TLSHandshakeTimeout →             │
             │                         │
             └─────────────────────────┘
                         │
                         ▼
④ 写 HTTP Request
	
   request header
   request body
                         │
                         ▼
⑤ request 完全写完

          从这里开始
          ResponseHeaderTimeout
                 │
                 ▼
⑥ 等 Backend 返回 response header

HTTP/1.1 200 OK
Content-Type: ...
Content-Length: ...
                 │
                 ▼
⑦ 开始读取 response body

xxxxxxxxxxxxxxxxxxxx
xxxxxxxxxxxxxxxxxxxx

                 │
                 ▼
⑧ body EOF

                 │
                 ▼
⑨ TCP connection 放回连接池

                 │
           IdleConnTimeout
                 │
                 ▼
     太久没人用 -> close
```





### 配置一、ConnectTimeout

```
t.DialContext = (&net.Dialer{
    Timeout: settings.ConnectTimeout.Duration(),
}).DialContext
```

建立网络连接最多允许多长时间。

```
type Dialer struct {
	// Timeout is the maximum amount of time a dial will wait for
	// a connect to complete. If Deadline is also set, it may fail
	// earlier.
	//
	// The default is no timeout.
	//
	// When using TCP and dialing a host name with multiple IP
	// addresses, the timeout may be divided between them.
	//
	// With or without a timeout, the operating system may impose
	// its own earlier timeout. For instance, TCP timeouts are
	// often around 3 minutes.
	
	Timeout time.Duration
	一次尝试建立连接最大的时间，就算你设置10s，仍然可能不到10秒，建立连接就失败，不是说一定10秒才失败。
	默认值是没有timeout
	不管有没有，操作系统可能强制推行(impose)操作系统级别的timeout. 例如tcp的超时大约是3分钟。

```



也就是说这个timeout是app层面上的，操作系统也有。如果设置为10秒，举例，然后一个host查询到多个，然后一个一个尝试的时候，这个参数是总的尝试连接超时，而不是每个ip独立的10秒。

