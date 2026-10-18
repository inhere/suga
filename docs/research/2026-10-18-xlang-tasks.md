# 跨语言探索评测题库：18 道只读探索题与标准答案（第十八批 L1）

> 状态：题库（调研产物，非设计文档，不过文档校验器）
> 日期：2026-10-18
> 关联：`docs/research/2026-10-18-explore-compare.md`（探索方式 A/B）、`docs/research/2026-10-18-explore-efficiency.md`（基线与复测）
> 用途：给后续「suga 只读探索」评测提供固定题目与标准答案；**本任务只出题，不运行评测**（评测下一步另派）
> 仓库快照：相对工作空间根 `D:\work\inhere\my-tools-dev` 的路径，见 §1

## 1. 快照、口径与判分约定

### 1.1 仓库快照（已固定提交，不 pull、不修改）

| 题号前缀 | repo_dir（相对工作空间根） | 语言/项目 | 提交 | 生产代码范围 | 需排除 |
|---|---|---|---|---|---|
| RQ | `tmp/xlang/requests` | Python · requests | `611c616` | `src/requests/` | `tests/`、`docs/`、`ext/` |
| KY | `tmp/xlang/ky` | TypeScript · ky | `3541888` | `source/` | `test/`、`test-d/`、`media/` |
| GS | `tmp/xlang/gson` | Java · Gson | `e2292f9` | `gson/src/main/java/` | `gson/src/test/`、`test-*`、`extras/`、`metrics/`、`proto/` |
| GZ | `tmp/xlang/guzzle` | PHP · Guzzle | `e3ec346` | `src/` | `tests/`、`docs/` |
| WD | `tmp/xlang/walkdir` | Rust · walkdir（suga 不支持的语言） | `6fd031c` | `src/lib.rs`、`src/dent.rs`、`src/error.rs`、`src/util.rs`、`walkdir-list/` | `src/tests/`、`compare/` |
| ZL | `inhere.github.io` | Zola 站点 + tabi 主题（子模块 `themes/tabi/`） | `269a9ff` | `config.toml`、`templates/`、`themes/tabi/templates/`、`static/` | `public/`、`output/`、`tmp/`（构建产物，已 gitignore） |

### 1.2 题型

| 类型 | 含义 | 本批设计要点 |
|---|---|---|
| D 找定义 | 某个具体类/函数/方法/组件定义在哪个文件哪一行、签名是什么 | 定义唯一但同族重载/近名多（`fromJson` 12 个重载、`sort_by`/`sort_by_key`/`sort_by_file_name`），必须给出**签名文本**才算答对 |
| C 找调用方 | 某个具体方法被哪些**生产代码**直接调用（文件:行） | 每题的答案集是 3–6 处，且**同名/近名干扰**充分：名字在其他类里也有定义、或近似名字（`requestAsync`、`sendAsync`、`isErrorType`、`rel_attributes`）会产生假阳性；必须排除测试、声明、文档注释示例 |
| F 追配置/选项生效 | 某个用户可见选项从入口到真正生效，按调用顺序列出 3–6 个位置 | 链路上必须有“记忆点”与“生效点”两类：只给出记忆点（构造器/默认值）不算完整 |

### 1.3 判分约定（对每个任务都适用）

- **行号口径**：以 §1.1 的固定提交为准，答对要求文件名与行号完全一致（不设容差）。同一行有多种表述（`src/requests/sessions.py:154` 与 `src/requests/sessions.py:154-155`）都算命中。
- **必中项**：每题给出「必中」集合，缺一即该题不通过（严格判分）；「可选」项记入加分/扣分参考，不影响通过与否。
- **假阳性判罚**：题目答案里凡列出「陷阱位置」（见每题「近似匹配陷阱」）中的任一位置，该题直接判错——C、D 题都是这样设计的。
- **F 题顺序**：要求按调用/渲染顺序递增；顺序错乱判错（两处相邻同一函数的行可互换，题内会注明）。
- **工具无关**：提问原文只描述代码事实，不提示用什么工具、不给路径前缀。

## 2. 题库总表

| id | 仓库 | 类型 | 提问原文（发给被测 agent，中文一句话） | 标准答案（文件:行） | 判分规则（必中 / 可选 / 陷阱） |
|---|---|---|---|---|---|
| RQ1 | requests | D | requests 在重定向时用来判断「是否要把原请求的 Authorization 头删掉」的方法叫什么，它定义在哪个文件哪一行，签名是什么？ | `src/requests/sessions.py:154`：`def should_strip_auth(self, old_url: str, new_url: str) -> bool:` | 必中 `sessions.py:154` + 签名 + 方法名；可选：所属类 `SessionRedirectMixin`（`sessions.py:127`，不是 `Session`）；陷阱：`rebuild_auth`（`sessions.py:309`，唯一调用点 `:324`）被当成定义 |
| RQ2 | requests | C | requests 的 `Response.close()` 在仓库生产代码里被哪几处**直接调用**？给出文件:行。 | 3 处：`src/requests/models.py:816`、`src/requests/sessions.py:222`、`src/requests/auth.py:301`（定义 `src/requests/models.py:1173`） | 必中 3 处；陷阱：`models.py:1180`（`self.raw.close()`，urllib3）、`sessions.py:509`（`Session.__exit__` 里的 `self.close()`）、`sessions.py:886`（`Session.close` 里的 `v.close()`，adapter）、`utils.py:323`（`os.close`）、`adapters.py:153`/`adapters.py:555`（两个同名 `close` 定义）；含任一即判错 |
| RQ3 | requests | F | `trust_env` 这个开关（Session 上的属性）从设置它的地方到真正影响代理与证书选择的地方，按执行顺序经过哪几处？给出 4–6 处文件:行。 | 1) `src/requests/sessions.py:492`（`Session.__init__` 默认 `True`）2) `sessions.py:537`（`Session.prepare_request` 里决定是否用 netrc 环境认证）3) `sessions.py:641`（`Session.request` 调 `merge_environment_settings`）4) `sessions.py:845`（`merge_environment_settings` 里 `if self.trust_env:`：环境代理 + `REQUESTS_CA_BUNDLE`）5) `sessions.py:763`（`Session.send` 调 `resolve_proxies(..., self.trust_env)`）6) `src/requests/utils.py:932`（`resolve_proxies` 里 `if trust_env and not should_bypass_proxies(...)` → `get_environ_proxies`） | 必中 4 处：`492`、`845`、`763`、`utils.py:932`；可选：`537`、`641`（这两处都在 request 之前，顺序须递增 492<537<641<845<763<932）；额外可提项：`utils.py:810`（`should_bypass_proxies` 定义，是第 6 步的被调方，提与不提都不影响通过） |
| KY1 | ky | D | ky 里负责按 `retry.delay`、`retry.jitter`、`retry.backoffLimit` 算出「这一次重试要等多少毫秒」的私有方法是哪个，定义在哪个文件哪一行，签名是什么？ | `source/core/Ky.ts:665`：`#calculateDelay(retry: InternalOptions['retry'], error: unknown): number {` | 必中 `Ky.ts:665` + 方法名 `#calculateDelay` + 签名；陷阱：紧邻的 `Ky.ts:688` `async #calculateRetryDelay(error: unknown)`（另一个方法，内部调用 `#calculateDelay`，调用点 `Ky.ts:699`、`:730`、`:743`、`:754`、`:762`） |
| KY2 | ky | C | 判断一个值是不是 `Error` 的那个 `isError`，在 ky 的生产代码里被哪些地方**直接调用**？给出文件:行（不含定义与 import）。 | 5 处：`source/core/Ky.ts:785`、`Ky.ts:806`、`Ky.ts:1178`、`source/errors/NonError.ts:13`、`source/utils/is-network-error.ts:17`（定义 `source/utils/is.ts:13`） | 必中 5 处；陷阱：`source/utils/type-guards.ts:9` 的 `isErrorType`（前缀同名，自身调用在 `type-guards.ts:60`、`:82`、`:104`、`:131`、`:154`），以及 `isKyError`/`isHTTPError`/`isNetworkError`/`isResponseSizeError` 等 `is*Error` 家族；行首注释（`Ky.ts:1177`、`Ky.ts:784`）里提到 `isError` 不算调用点；含任一即判错 |
| KY3 | ky | F | 用户传入的 `timeout` 选项，从 `ky(url, {timeout: 1000})` 这个实例入口到真正创建定时器为止，按调用顺序经过哪几处？给出 5–6 处文件:行。 | 1) `source/index.ts:35`（`ky(input, options)` → `createRequest(input, () => validateAndMerge(defaults, options))`）2) `source/utils/merge.ts:70`（`validateAndMerge` 用 `deepMerge` 合并各层，`timeout` 作为普通键被拷贝）3) `source/core/Ky.ts:494`（构造 `this.#options` 时 `...withAttemptDefaults(options)` 补默认 `timeout = 10_000`，见 `Ky.ts:218`）4) `source/core/Ky.ts:501`（`validateTimeoutOption(this.#options.timeout, 'timeout')` 校验）5) `source/core/Ky.ts:1244`（`#fetch` 里 `await timeout(request, nonRequestOptions, this.#abortController, {timeout: effectiveTimeout, ...})`，有效值取自 `Ky.ts:1238`）6) `source/utils/timeout.ts:28`（`setTimeout` 用它计时，定时器创建在 `timeout.ts:22`，到点 abort + 抛 `TimeoutError`） | 必中 4 处：`index.ts:35`、`Ky.ts:501`、`Ky.ts:1244`、`timeout.ts:22` 或 `:28`；可选：`merge.ts:70`、`Ky.ts:494`、`Ky.ts:1238`（有效超时折算）；顺序须递增；陷阱：把 `merge.ts:446`（`deepMerge` 内部拷贝点）或 `Ky.ts:218`（`withAttemptDefaults` 默认值定义）当成「生效点」 |
| GS1 | gson | D | Java 的 `Gson` 类里那个接收 `JsonReader` 和 `Type`、把 JSON 读成对象的 `fromJson` 重载定义在哪个文件哪一行？签名原文是什么？ | `gson/src/main/java/com/google/gson/Gson.java:1046-1047`：`public <T> T fromJson(JsonReader reader, Type typeOfT)` / `throws JsonIOException, JsonSyntaxException {` | 必中 `Gson.java:1046` + 参数列表 `(JsonReader reader, Type typeOfT)`；必中项含泛型 `<T> T`；`1047` 的 `throws` 可选；陷阱：`Gson.java:1087` 的 `fromJson(JsonReader, TypeToken)` 与同文件其余 10 个 `fromJson` 重载（`:850`、`:879`、`:909`、`:940`、`:971`、`:1001`、`:1159`、`:1186`、`:1212`）、`stream/TypeAdapter.java:211`/`:230` 的同名方法；答成其中任一行即判错 |
| GS2 | gson | C | `com.google.gson.internal.Streams.write(JsonElement, JsonWriter)`（把 JSON 树写进 writer）在生产代码里被哪些地方**直接调用**？给出文件:行。 | 4 处：`Gson.java:767`、`JsonElement.java:428`、`internal/bind/MapTypeAdapterFactory.java:248`、`internal/bind/TreeTypeAdapter.java:109`（定义 `internal/Streams.java:69`） | 必中 4 处（分布在 4 个文件）；陷阱：`TypeAdapter.java:131`（`abstract void write(JsonWriter, T)`）、`internal/bind/ArrayTypeAdapter.java:98`、`internal/bind/CollectionTypeAdapterFactory.java:92`、`internal/bind/DefaultDateTypeAdapter.java:140`、`internal/bind/TypeAdapters.java:72`/`:136`/`:164`（该文件里十余个匿名 adapter 各有一个）、`internal/Excluder.java:138` 等 adapter 的 `write(...)`，以及 `Streams.java:88`、`:111`、`:116` 内部对 `java.io.Writer.write` 的调用；`gson/src/test/.../RuntimeTypeAdapterFactoryFunctionalTest.java:224` 是测试，不计 |
| GS3 | gson | F | `new GsonBuilder().disableHtmlEscaping()` 这个设置从 builder 到真正决定「是否把 HTML 字符转义成 `\u003c` 之类」的地方，依次经过哪几处？给出 5 处文件:行。 | 1) `GsonBuilder.java:614`（`public GsonBuilder disableHtmlEscaping()` 入口；`:615` 写入 builder 字段，字段声明 `GsonBuilder.java:144`）2) `Gson.java:254`（`Gson(GsonBuilder)` 里 `this.htmlSafe = builder.escapeHtmlChars;`）3) `Gson.java:685`（`toJson(Object, Type, JsonWriter)` 写出前 `writer.setHtmlSafe(htmlSafe);`）4) `stream/JsonWriter.java:368`（`setHtmlSafe` 落到 writer 字段，方法定义 `:367`）5) `stream/JsonWriter.java:744`（`string(String)` 里 `String[] replacements = htmlSafe ? ... : ...` 选转义表 = 真正生效点） | 必中 5 处：`GsonBuilder.java:614` 或 `:615`、`Gson.java:254`、`Gson.java:685`、`JsonWriter.java:368`、`JsonWriter.java:744`；顺序须递增（写 builder → 构造 Gson → 写出时推给 writer → writer 字段 → 转义时选表）；陷阱：把 `GsonBuilder.java:144`（字段声明）、`JsonWriter.java:367`（方法定义行）当生效点可接受为相邻行，但把 `JsonWriter.isHtmlSafe()` 当生效点判错；注意树模型 `JsonTreeWriter` 不经过 `:744`（可作可选加分说明） |
| GZ1 | guzzle | D | Guzzle 的 `Client` 里读取客户端配置项（只取某一项或取全部）的方法定义在哪个文件哪一行？签名原文是什么？ | `src/Client.php:672`：`public function getConfig(?string $option = null)`（方法体 `:674-676`；`@return` 注释在 `:670`，无 PHP 返回类型声明） | 必中 `src/Client.php:672` + 签名（含默认参数 `= null`）；可选：说明它没有返回类型声明；陷阱：把 `Client::getConfig` 之外的东西当答案（`getConfig` 在 `src/` 内只有这一处定义，`ClientInterface` 未声明它）；答成 `:673`（方法体开括号）判错 |
| GZ2 | guzzle | C | `ClientTrait::request()` 这个抽象方法在生产代码里被哪些地方**直接调用**？给出文件:行。 | 6 处（都在 `src/ClientTrait.php`，即 `get/head/put/post/patch/delete` 六个别名方法）：`ClientTrait.php:202`（GET）、`:295`（HEAD）、`:388`（PUT）、`:481`（POST）、`:574`（PATCH）、`:667`（DELETE） | 必中 6 处；陷阱：同名**声明**`src/Client.php:650`（`Client::request`）、`src/ClientTrait.php:110`（`abstract public function request`）、`src/ClientInterface.php:281`（接口声明）被当成调用点；近似名 `requestAsync`（定义 `src/Client.php:527`、`ClientTrait.php:756`、`ClientInterface.php:369`，调用 `src/Client.php:658`）与 `sendAsync`（`src/Client.php:422`、`:438`）被当成命中；含任一即判错 |
| GZ3 | guzzle | F | 用户传入的 `http_errors` 选项从 `Client::request()` 入口到真正决定「响应非 2xx 时是否抛异常」的地方，按调用顺序经过哪几处？给出 4–6 处文件:行。 | 1) `src/Client.php:853`（`configureDefaults` 里的默认值 `'http_errors' => true,`；构造期调用点 `:236`）2) `src/Client.php:943`（`prepareDefaults`（定义 `:914`）里 `$result = $options + $defaults;` 把默认垫在用户选项下；调用点 `requestAsync :533`）3) `src/Client.php:1004`（`assertRequestOptionTypes`（定义 `:957`）里 `assertIfPresentAndNotBool($options, 'http_errors')`；调用点 `:952`）4) `src/Client.php:1514`（`transfer`（定义 `:1499`）把 options 交给 handler 栈）5) `src/Middleware.php:97`（`Middleware::httpErrors` 闭包里 `if (empty($options['http_errors']))` = 真正生效点） | 必中 4 处：`Client.php:943`、`Client.php:1004`、`Client.php:1514`、`Middleware.php:97`；可选：`Client.php:853`、`src/HandlerStack.php:59`（`$stack->push(Middleware::httpErrors(), 'http_errors')`，注册发生在客户端**构造期**，时间上早于第 1 步）与 `Middleware.php:88`（中间件工厂定义）；陷阱：`Client::sendRequest` 里 `src/Client.php:436` 硬编码 `$options[RequestOptions::HTTP_ERRORS] = false;`（异步分支，不是本题入口链路），以及 `Client.php:853-...` 里其他默认项 |
| WD1 | walkdir | D | walkdir 里给目录项自定义排序的 `WalkDir::sort_by` 定义在哪个文件哪一行？完整签名（含 where 约束）是什么？ | `src/lib.rs:417-420`：`pub fn sort_by<F>(mut self, cmp: F) -> Self` / `where` / `    F: FnMut(&DirEntry, &DirEntry) -> Ordering + Send + Sync + 'static,` / `{` | 必中 `src/lib.rs:417` + 泛型 `<F>` + `where` 约束（`FnMut(&DirEntry, &DirEntry) -> Ordering + Send + Sync + 'static`）；陷阱：同文件 `:439` `sort_by_key`、`:456` `sort_by_file_name` 被当成答案，或把 `src/lib.rs:915` 的 `entries.sort_by(...)`（`Vec` 的排序，不是 `WalkDir` 的构建方法）当定义；答 `:418`/`:419` 算作相邻行可接受，但必须同时给出签名文本 |
| WD2 | walkdir | C | walkdir 自己实现的 `DirEntry::file_type()`（定义在 `src/dent.rs`）在 `src/` 的生产代码里被哪些地方**直接调用**？给出文件:行（排除 `src/tests/` 下的测试与文档注释里的示例）。 | 3 处（接收者都是 walkdir 的 `DirEntry`，都在 `src/lib.rs`）：`lib.rs:844`（`if self.opts.follow_links && dent.file_type().is_symlink() {`）、`lib.rs:847`（`let is_normal_dir = !dent.file_type().is_symlink() && dent.is_dir();`）、`lib.rs:857`（`&& dent.file_type().is_symlink()`）；定义 `src/dent.rs:158`（`pub fn file_type(&self) -> fs::FileType`） | 必中 3 处；陷阱：`src/lib.rs:870`（`md.file_type()`，`md: fs::Metadata`，标准库）、`src/dent.rs:191`/`:207`/`:224`（`ent.file_type()`，`ent: fs::DirEntry`，标准库）、`src/lib.rs:767-768` 与 `:1177-1178`（文档注释里的示例）、`src/tests/recursive.rs` 里数十处（测试）；含任一即判错 |
| WD3 | walkdir | F | `WalkDir::new(path).follow_links(true)` 这个选项从设置它的地方到真正决定「是否跟随符号链接」的地方，按执行顺序经过哪几处？给出 4–6 处文件:行。 | 1) `src/lib.rs:346-347`（`pub fn follow_links(mut self, yes: bool) -> Self` / `self.opts.follow_links = yes;`，入口写入 `Options`）2) `src/lib.rs:844`（`IntoIter::handle_entry` 里 `if self.opts.follow_links && dent.file_type().is_symlink() {`）3) `src/lib.rs:845` → `src/lib.rs:961-963`（`dent = itry!(self.follow(dent));` 调 `fn follow`，内部 `DirEntry::from_path(self.depth, dent.path().to_path_buf(), true)` 按目标重取元数据）4) `src/lib.rs:967-968`（`if dent.is_dir() { self.check_loop(dent.path())?; }` 防符号链接环，查环用 `stack_path`）5) `src/lib.rs:923`（`IntoIter::push` 里 `if self.opts.follow_links {` 把 ancestor 压栈）6) `src/lib.rs:952`（`IntoIter::pop` 里 `if self.opts.follow_links {` 弹栈） | 必中 5 处：`lib.rs:347`、`lib.rs:844`、`lib.rs:961`（或 `:963`）、`lib.rs:923`、`lib.rs:952`；可选：`lib.rs:845`（`follow` 的调用点）、`lib.rs:968`（查环）；顺序须递增（347 → 844 → 961 → 923 → 952）；陷阱：把 `src/dent.rs:26`/`:33`/`:107` 等文档注释里提到的 `follow_links` 当代码位置，把 `lib.rs:240`（`Options` 字段声明）或 `lib.rs:292`（默认 `follow_links: false`）当生效点 |
| ZL1 | inhere.github.io | D | 站点里决定「某个设置该取 page/section/config 哪一层值」的 Tera 组件 `evaluate_setting_priority` 定义在哪个文件哪一行？它的参数列表是什么？ | `themes/tabi/templates/components/settings.html:12`：`{% component evaluate_setting_priority(setting: string, @page = {}, @config: map, @lang: string, @section = {}, default_global_value = "") %}`（结束标签 `:83`，说明注释 `:1-10`） | 必中文件路径 + `:12` + 参数列表（至少 `setting`、`@page`、`@config`、`@lang`、`@section`、`default_global_value`）；陷阱：这是调用点最多的组件（20+ 处），把某个**调用点**（如 `page.html:162`、`partials/header.html:4`、`partials/extra_features.html:6`）当定义即判错；站点 `templates/` 下没有它的覆盖副本 |
| ZL2 | inhere.github.io | C | Tera 组件 `target_attribute` 被仓库里哪些模板**调用**？给出文件:行（不含定义处）。 | 5 处调用：`themes/tabi/templates/page.html:8`、`themes/tabi/templates/components/page_header.html:6`、`themes/tabi/templates/partials/footer.html:5`、`themes/tabi/templates/partials/cards_pages.html:10`、`templates/partials/cards_pages.html:16`（站点对主题同名 partial 的覆盖副本）；定义 `themes/tabi/templates/components/target_attribute.html:1`（`{% component target_attribute(new_tab: bool) %}`） | 必中 5 处（主题 4 处 + 站点覆盖 1 处）；漏掉站点覆盖的 `templates/partials/cards_pages.html:16` 即判错（同名 partial 两份是本题的干扰核心）；陷阱：把定义行 `components/target_attribute.html:1`、结束标签 `:11`、站点覆盖文件的注释行 `templates/partials/cards_pages.html:4` 当调用；把**近名**组件 `rel_attributes`（定义 `themes/tabi/templates/components/rel_attributes.html:1`）的 5 处调用（`themes/tabi/templates/page.html:6`、`components/page_header.html:3`、`partials/cards_pages.html:1`、`partials/footer.html:3`、`templates/partials/cards_pages.html:6`）算成 `target_attribute` 的调用；`public/`、`output/` 是构建产物（gitignore），不算 |
| ZL3 | inhere.github.io | F | `config.toml` 里的 `extra.stylesheets`（站点额外样式表）从配置到出现在页面 `<head>` 的 `<link>` 上，按渲染顺序经过哪几处？给出 3–5 处文件:行。 | 1) `config.toml:108`（`[extra]` 段的 `stylesheets = ["custom.css"]`，段首 `:88`）2) `themes/tabi/templates/base.html:24`（`{% include "partials/header.html" %}`，各页面模板都经 `base.html`）3) `themes/tabi/templates/partials/header.html:63` 与 `:64`（`{%- if config.extra.stylesheets -%}` / `{%- set stylesheets = [...stylesheets, ...config.extra.stylesheets] -%}` 追加进列表）4) `themes/tabi/templates/partials/header.html:78-79`（`for` 循环渲染 `<link rel="stylesheet" href="{{ get_url(path=stylesheet, cachebust=true) }}">`）5) `static/custom.css`（`get_url` 解析到的实际文件，站点根 `static/` 下确实存在） | 必中 3 处：`config.toml:108`、`partials/header.html:63`（或 `:64`）、`partials/header.html:78-79`；可选：`base.html:24`、`static/custom.css`、`partials/header.html:73-74`（同机制的另一条链 `config.extra.skin`，可作为加分对照）；顺序须递增；陷阱：把 `partials/header.html:50`（`stylesheets = []` 初始化）或 `:60`（内置 `main.css`）当生效点、把 `:68`（页面级 `page.extra.stylesheets`）当成 `config.extra.stylesheets` |

## 3. 逐题干扰说明（给判分方）

### 3.1 requests

- RQ1：`def should_strip_auth` 全仓只有 `sessions.py:154` 一处定义，但它挂在 **`SessionRedirectMixin`** 上（`sessions.py:127`），不是 `Session`；唯一调用点 `sessions.py:324`（在 `rebuild_auth` 内，`rebuild_auth` 定义 `:309`）。把 `rebuild_auth` 当答案是最可能的错法。
- RQ2：`src/requests/` 下 `def close` 共 4 处（`models.py:1173` Response、`sessions.py:883` Session、`adapters.py:153` BaseAdapter、`adapters.py:555` HTTPAdapter）；`.close()` 调用共 6 处（另有 `utils.py:323` 的 `os.close(fd)`），全部列出如下，可据此核对假阳性：

  | 调用点 | 接收者 | 是否本题答案 |
  |---|---|---|
  | `models.py:816` | `Response` 自身（`__exit__`） | ✅ |
  | `sessions.py:222` | `Response`（`resolve_redirects` 释放连接） | ✅ |
  | `auth.py:301` | `Response`（`HTTPDigestAuth.handle_401`） | ✅ |
  | `models.py:1180` | `self.raw`（urllib3 response） | ❌ |
  | `sessions.py:509` | `Session` 自身（`__exit__`） | ❌ |
  | `sessions.py:886` | `v`（adapter） | ❌ |

- RQ3：`trust_env` 这个标识在 `src/requests/` 里共 12 处：`sessions.py:129`（mixin 上的类型声明）、`:330`（`rebuild_auth` 里决定重定向后是否重取 netrc）、`:353`（`rebuild_proxies` 里调用 `resolve_proxies`）、`:423`（`Session` 上的类型声明）、`:438`（属性清单里的字符串）、`:492`（默认值）、`:537`、`:763`、`:845`，以及 `utils.py:914`（`resolve_proxies` 的形参）、`:922`（文档）、`:932`（读取）。本题只认「影响代理/证书/认证」的执行链；重定向路径（`:330`、`:353`）与 `utils.py:810` `should_bypass_proxies`（第 6 步的被调方，定义行）可在作答中提，但不算必中。

### 3.2 ky

- KY1：`#calculateDelay`（`Ky.ts:665`）与 `#calculateRetryDelay`（`Ky.ts:688`）相邻且近名，前者负责算毫秒数（`retry.delay` → 校验非负数 → `jitter` → `Math.min(backoffLimit, ...)`，`Ky.ts:666-685`），后者是重试流程的调度者。答 `#calculateRetryDelay` 直接判错。
- KY2：`isError(` 在 `source/` 下只有 5 个调用点（本节已用词界匹配核对）；干扰主要是 `utils/type-guards.ts:9` 的 `isErrorType`（它自身被调用 5 次）以及注释里提到的 `isError`（`Ky.ts:784`、`:1177`、`utils/is.ts:12`、`errors/NonError.ts:10`）。
- KY3：链路里最容易被漏的是入口 `index.ts:35`（`ky` 实例把用户 options 与 defaults 一起交给 `validateAndMerge`），以及第 5 跳（`#fetch` → `utils/timeout.ts`）；`merge.ts:446`（`deepMerge` 内部逐键拷贝）与 `Ky.ts:218`（`withAttemptDefaults` 默认值）都不是生效点。

### 3.3 gson

- GS1：`Gson.java` 里 `fromJson` 有 12 个重载，只有 `:1046` 是 `(JsonReader, Type)`；`:1087` 是 `(JsonReader, TypeToken)`，最容易混。
- GS2：`Streams.write(` 在 `gson/src/main/java/` 下只有 4 个调用点（分布在 4 个文件）；`extras/`、`metrics/`、`proto/` 无命中；测试里唯一一处在 `gson/src/test/.../RuntimeTypeAdapterFactoryFunctionalTest.java:224`。干扰面极大的是各类 `TypeAdapter.write(JsonWriter, T)` 的实现与调用（`TypeAdapter.java:131` 等），以及 `internal/Streams.java:88`、`:111`、`:116` 对 `java.io.Writer.write` 的调用。
- GS3：链路 5 跳都可用「写 builder → 构造 Gson 快照 → 写出前推给 writer → writer 字段 → 转义时选表」复述；`GsonBuilder.java:144` 是字段声明、`JsonWriter.java:367` 是 setter 定义，允许作为相邻行；树模型写出（`JsonTreeWriter`）不做字符转义，不经 `JsonWriter.java:744`，可作可选加分说明。

### 3.4 guzzle

- GZ1：`getConfig` 在 `src/` 内唯一定义（`Client.php:672`），`ClientInterface` 没有它，本体在 `:674-676`，且**没有** PHP 返回类型声明（类型信息在 `:670` 的 `@return`）。
- GZ2：`$this->request(` 在生产代码里恰好 6 处，全在 `ClientTrait.php`（六个 HTTP 动词别名）；同名的**抽象声明/接口声明**在 `ClientTrait.php:110`、`ClientInterface.php:281`，`Client` 自己的实现定义在 `Client.php:650`，都极易被误算；近似名 `requestAsync`（`Client.php:527` 等）与 `sendAsync` 会产生假阳性。
- GZ3：`http_errors` 的读取点只有一个（`Middleware.php:97`），但链路上有两次「选项被搬运」（`Client.php:943` 合并、`Client.php:1514` 交给 handler），以及 `Client.php:853` 的默认值；`HandlerStack.php:59` 的中间件注册发生在客户端构造期，时间上最早，判卷时按「可选」处理，避免顺序争议。

### 3.5 walkdir（suga 不支持的语言，纯文本核对）

- WD1：`sort_by`（`lib.rs:417`）、`sort_by_key`（`:439`）、`sort_by_file_name`（`:456`）三个构建方法相邻；签名跨 4 行，必须带 `where` 约束。`lib.rs:915` 的 `entries.sort_by(...)` 是 `Vec` 的调用，不是定义。
- WD2：`file_type()` 是全库最容易「近似匹配跑偏」的名字：walkdir 自己的 `DirEntry::file_type`（`dent.rs:158`）在生产代码里只被调用 3 次（`lib.rs:844`、`:847`、`:857`），而标准库的 `fs::Metadata::file_type`（`lib.rs:870`）、`fs::DirEntry::file_type`（`dent.rs:191`、`:207`、`:224`）与测试里的数十处会一起被捞出来。
- WD3：`follow_links` 在代码里是「构建方法 `lib.rs:346` + `Options` 字段 `lib.rs:240` + 3 处 `self.opts.follow_links` 读取（`lib.rs:844`、`:923`、`:952`）」，另有大量文档注释提及（`dent.rs:26`/`:33`/`:107`/`:152` 等），只有 `lib.rs:844` 是决定是否跟随的那一次判断。

### 3.6 inhere.github.io（Zola 站点）

- ZL1：`evaluate_setting_priority` 的调用点 20+ 处（`base.html:17`、`page.html` 十余处、`partials/extra_features.html`、`partials/content_security_policy.html`、`cards.html:21`、`series.html:17` 等），定义只有 `components/settings.html:12` 一处。
- ZL2：`target_attribute` 的调用点 5 处，其中 4 处在主题、1 处在站点覆盖文件 `templates/partials/cards_pages.html:16`（该文件是对主题 `themes/tabi/templates/partials/cards_pages.html` 的同名覆盖，文件头注释说明了原因）。近名组件 `rel_attributes` 同样有 5 处调用（`page.html:6`、`components/page_header.html:3`、`partials/cards_pages.html:1`、`partials/footer.html:3`、站点 `templates/partials/cards_pages.html:6`），行号与 `target_attribute` 的调用点一一错位（`page.html` 6 对 8、`page_header.html` 3 对 6、主题 `cards_pages.html` 1 对 10、`footer.html` 3 对 5、站点 `cards_pages.html` 6 对 16），是本题假阳性的主要来源；此外两个组件的返回值都被赋给了同名局部变量 `rel_attributes` / `blank_target`，`rel="{{ rel_attributes }}"` 之类的变量引用不是调用点。
- ZL3：`extra.stylesheets` 的搬运点集中在 `partials/header.html`（`:50` 初始化 → `:60` 内置 `main.css` → `:63-64` 站点配置 → `:68-69` 页面级 → `:73-74` skin → `:78-79` 渲染），只有 `:63-64` 与 `:78-79` 属于本链；`static/custom.css` 确实存在，是链路的终点。

## 4. 附表（tmp-free，机器可读）

`repo_dir` 相对工作空间根 `D:\work\inhere\my-tools-dev`；`提问` 可直接作为 `suga run --read-only -p "<提问>"` 的 prompt。

```tsv
id	repo_dir	提问
RQ1	tmp/xlang/requests	requests 在重定向时用来判断「是否要把原请求的 Authorization 头删掉」的方法叫什么，它定义在哪个文件哪一行，签名是什么？
RQ2	tmp/xlang/requests	requests 的 Response.close() 在仓库生产代码里被哪几处直接调用？给出文件:行。
RQ3	tmp/xlang/requests	trust_env 这个开关（Session 上的属性）从设置它的地方到真正影响代理与证书选择的地方，按执行顺序经过哪几处？给出 4-6 处文件:行。
KY1	tmp/xlang/ky	ky 里负责按 retry.delay、retry.jitter、retry.backoffLimit 算出「这一次重试要等多少毫秒」的私有方法是哪个，定义在哪个文件哪一行，签名是什么？
KY2	tmp/xlang/ky	判断一个值是不是 Error 的那个 isError，在 ky 的生产代码里被哪些地方直接调用？给出文件:行（不含定义与 import）。
KY3	tmp/xlang/ky	用户传入的 timeout 选项，从 ky(url, {timeout: 1000}) 这个实例入口到真正创建定时器为止，按调用顺序经过哪几处？给出 5-6 处文件:行。
GS1	tmp/xlang/gson	Java 的 Gson 类里那个接收 JsonReader 和 Type、把 JSON 读成对象的 fromJson 重载定义在哪个文件哪一行？签名原文是什么？
GS2	tmp/xlang/gson	com.google.gson.internal.Streams.write(JsonElement, JsonWriter)（把 JSON 树写进 writer）在生产代码里被哪些地方直接调用？给出文件:行。
GS3	tmp/xlang/gson	new GsonBuilder().disableHtmlEscaping() 这个设置从 builder 到真正决定「是否把 HTML 字符转义成 \u003c 之类」的地方，依次经过哪几处？给出 5 处文件:行。
GZ1	tmp/xlang/guzzle	Guzzle 的 Client 里读取客户端配置项（只取某一项或取全部）的方法定义在哪个文件哪一行？签名原文是什么？
GZ2	tmp/xlang/guzzle	ClientTrait::request() 这个抽象方法在生产代码里被哪些地方直接调用？给出文件:行。
GZ3	tmp/xlang/guzzle	用户传入的 http_errors 选项从 Client::request() 入口到真正决定「响应非 2xx 时是否抛异常」的地方，按调用顺序经过哪几处？给出 4-6 处文件:行。
WD1	tmp/xlang/walkdir	walkdir 里给目录项自定义排序的 WalkDir::sort_by 定义在哪个文件哪一行？完整签名（含 where 约束）是什么？
WD2	tmp/xlang/walkdir	walkdir 自己实现的 DirEntry::file_type()（定义在 src/dent.rs）在 src/ 的生产代码里被哪些地方直接调用？给出文件:行（排除 src/tests/ 下的测试与文档注释里的示例）。
WD3	tmp/xlang/walkdir	WalkDir::new(path).follow_links(true) 这个选项从设置它的地方到真正决定「是否跟随符号链接」的地方，按执行顺序经过哪几处？给出 4-6 处文件:行。
ZL1	inhere.github.io	站点里决定「某个设置该取 page/section/config 哪一层值」的 Tera 组件 evaluate_setting_priority 定义在哪个文件哪一行？它的参数列表是什么？
ZL2	inhere.github.io	Tera 组件 target_attribute 被仓库里哪些模板调用？给出文件:行（不含定义处）。
ZL3	inhere.github.io	config.toml 里的 extra.stylesheets（站点额外样式表）从配置到出现在页面 <head> 的 <link> 上，按渲染顺序经过哪几处？给出 3-5 处文件:行。
```

## 5. 交付与后续

- 本文件是可执行的题库：§4 的 `提问` 列即 prompt，§2 的「标准答案 / 判分规则」列即评分依据；18 题全部有「必中集合 + 陷阱集合」，可支持严格判分与宽松判分两种口径。
- 行号的复核方式：全部用只读文件工具（`file_read` 摘原文 + 词界 grep 统计调用点）在本任务的仓库快照上一次核对完成；未跑任何评测、未运行被测仓库的构建或测试。
- 评测执行（跑 suga/omp、统计步数/token/假阳性率）不在本任务内，下一步另派。
