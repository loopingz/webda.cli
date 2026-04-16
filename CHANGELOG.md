# Changelog

## [1.1.0](https://github.com/loopingz/webda.cli/compare/v1.0.0...v1.1.0) (2026-04-16)


### Features

* auto-update system ([b70b213](https://github.com/loopingz/webda.cli/commit/b70b213859818f75507536fc5f2538833ba9b6f8))

## 1.0.0 (2026-04-16)


### Features

* add --generate-cli-skeleton and --input flags for operations ([78a2477](https://github.com/loopingz/webda.cli/commit/78a247797e68c091a38ea2e57c6214d0ee688169))
* add AES-256-GCM encrypt/decrypt helpers for token storage ([eec38d0](https://github.com/loopingz/webda.cli/commit/eec38d01ff4d633e2d73a797bfcd91e56df5e0f4))
* add camelToKebab and splitOperationName utilities ([ece3e7f](https://github.com/loopingz/webda.cli/commit/ece3e7ff92a60ef44de7189df4244dfedae428c3))
* add expires_in ([02e0a53](https://github.com/loopingz/webda.cli/commit/02e0a53c763e2960090d902908f261d3e69f8ef2))
* add KeyringStore for system keyring token storage ([9e52e21](https://github.com/loopingz/webda.cli/commit/9e52e21ed3781b189eaadd5778d80009ff60e267))
* add logo fetching, caching, and terminal rendering ([f650c03](https://github.com/loopingz/webda.cli/commit/f650c031d6bc7c0a3c383d14ee96b4cd560634d2))
* add MachineStore for machine-bound token encryption ([8e9c3f6](https://github.com/loopingz/webda.cli/commit/8e9c3f64bbab271df379498a46854c424d2d2b5f))
* add NewTokenStore factory with keyring &gt; ssh-agent &gt; machine fallback ([47df453](https://github.com/loopingz/webda.cli/commit/47df453472f6cc2f087ba1089d5db74e5ed19d93))
* add SSHAgentStore for SSH agent-based token encryption ([d1c190c](https://github.com/loopingz/webda.cli/commit/d1c190c621deff30c44af7321798f3621ad6523f))
* add TokenStore interface and TokenInfo type ([3ceb82e](https://github.com/loopingz/webda.cli/commit/3ceb82eb0b2f19dcdd5703f6c19b65aa992f5c9c))
* auto-install shell completion on first launch and update README ([b2e334d](https://github.com/loopingz/webda.cli/commit/b2e334dbf3014c962261f04ec320645cfde02b5d))
* auto-refresh ([99f4961](https://github.com/loopingz/webda.cli/commit/99f4961844389951e950b399e0314f3b8294e5e8))
* build nested cobra command tree from operations with schema flags ([13df407](https://github.com/loopingz/webda.cli/commit/13df407b7387218e11a92edb1c3684ea1a70e8d4))
* capture inline input/output JSON schemas in Operation struct ([6fbb90c](https://github.com/loopingz/webda.cli/commit/6fbb90c1ccb29cd55bdbdd0b0568618ae3692de5))
* first commit ([894478c](https://github.com/loopingz/webda.cli/commit/894478c576be3dac73bc7af3fc197d0c1adfe4bc))
* implement operation execution via POST /operations/{id} ([cd91579](https://github.com/loopingz/webda.cli/commit/cd915791dfa9b3fbdc202a3110cfa21666beaf18))
* integrate command tree, TUI forms, and logo into CLI ([b276049](https://github.com/loopingz/webda.cli/commit/b27604942d664bf6de65e2c57e1e723315f53927))
* integrate TokenStore into main.go, remove plaintext token handling ([a272856](https://github.com/loopingz/webda.cli/commit/a2728569b6c19fb3eb4206ec60c10d8fed462405))
* integrate TokenStore into webdaclient, remove plaintext file handling ([b446e12](https://github.com/loopingz/webda.cli/commit/b446e1253aef365ee4ac0df44c3ef7b8741efcf7))
* TUI form generation from JSON schemas using huh ([5f39e15](https://github.com/loopingz/webda.cli/commit/5f39e1569dce6acaf487e0157f4995d0b1d45891))
* wire TUI forms into operation execution with --interactive flag ([0f5e9ae](https://github.com/loopingz/webda.cli/commit/0f5e9aeae861da53770fdc7bd383f64f4c6c719c))


### Bug Fixes

* remove toolchain directive to fix CI Go version mismatch ([#2](https://github.com/loopingz/webda.cli/issues/2)) ([fa9e42e](https://github.com/loopingz/webda.cli/commit/fa9e42ef05b6a5f7a0b8614f57816ec0080d3128))
