// Rebuild the native admin asset without modifying the frozen donor build.
import {build} from 'esbuild';
await build({entryPoints:['internal/webshell/static_src/admin_console/admin_invitations.js'],bundle:true,format:'esm',target:'es2022',outfile:'internal/webshell/static/admin_console/admin_invitations.js'});
