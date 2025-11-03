# webda-cli


 A webda-cli is a cli client that have a default configuration based on the command name defined in `~/.webdacli/config.yaml`

 An example is:

```
wc: https://demo.webda.io
wc-local: http://localhost:18080
```

If `wc` is launched, it will then check inside the `~/.webdacli/` folder for its auth token for the command. If not found, it launch a browser to `https://demo.webda.io/auth/cli?callback=http://localhost:18181&name=wc-cli&hostname=...`, as it does that it will listen to port 18181 for a `POST /auth` request that will contain the new refresh_token, it will store it in `~/.webdacli/wc.tok`.

Then it will do a `GET https://demo.webda.io/operations` store it in `wc.operations`, read that file and define the different commands for the binary from there.