Build `owner_handoff_host.js` from this V3 Host source with:

`./node_modules/.bin/esbuild internal/webshell/static_src/admin_console/owner_handoff_host.ts --bundle --format=iife --global-name=OwnerHandoffHost --platform=browser --target=es2022 --outfile=internal/webshell/static/admin_console/owner_handoff_host.js`

The only import is the byte-frozen CSV/XLSX guard. The generated asset is embedded by webshell.
