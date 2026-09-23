# Webshell

`internal/webshell` owns the v3 presentation shell only:

- the complete admin navigation and shared base layout;
- the `/login` and `/auth/wecom/start` reserved login surfaces;
- the `/admin/config/login-access` reserved access entry;
- the `/admin/oneid` read-only OneID administration page;
- the production-derived `/admin/automation-conversion` audience-package shell and its `/packages/{id}` secondary configuration surface;
- the `/sidebar/bind-mobile` WeCom sidebar shell and its frozen bootstrap data URL attributes;
- embedded CSS, navigation icons, and the donor sidebar cover asset.

The standalone `Handler` is suitable for `httptest` and local previews. It does
not authenticate, issue sessions, read business data, call providers, or
connect the Composition Root. The rendered Access and OneID pages call only
their domain-owned endpoints when mounted by a composition root; the shell
does not register those API routes itself. Unimplemented sidebar tabs remain
local empty states.

The audience-package pages preserve the production DOM, CSS, menu structure,
and secondary-page navigation without mounting the production business APIs.
The list therefore renders an honest empty state, while data and mutation
controls on the secondary page stay disabled. The only page-specific browser
behavior is the production-derived local dimension switcher; it performs no
network request.

The OneID page sends only the frozen read-only lookup/list requests. Identity
values are not placed in URLs, rendered into result markup, persisted in
browser storage, or logged by the shell; customer details expose only the
server-provided identity kind/scope summaries.

The original shell presentation assets came from AI-CRM commit
`69c5282fb38058f2cc9872b6feb3f0f54bfad64b`. The audience-package templates,
their inline CSS, and `admin_console.js` were refreshed from AI-CRM commit
`dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f`; their source hashes were verified
against the deployed production release `41f80a11835445c034fdd39f69a6b6712722bb98`.
