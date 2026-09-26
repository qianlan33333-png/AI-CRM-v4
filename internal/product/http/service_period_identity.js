      const inWechat = /MicroMessenger/i.test(navigator.userAgent);
      const authAttemptKey = "aicrm.oauth.auto:" + window.location.pathname;
      const authURL = "/api/h5/wechat-pay/oauth/start?return_url=" +
        encodeURIComponent(window.location.pathname + window.location.search);
      let readGeneration = 0;
      let activeRead;

      function stateMessage(message, action, onClick, meta) {
        syncWecomAction({}, "none");
        setHeroDecor("", "");
        renderNone();
        const tip = document.createElement("p");
        tip.className = "service-period-tip";
        tip.setAttribute("role", "status");
        tip.setAttribute("aria-live", "polite");
        tip.textContent = message;
        card.appendChild(tip);
        barMeta.textContent = meta || action;
        button.textContent = action;
        button.disabled = !onClick;
        button.onclick = onClick || null;
      }

      function authorize(automatic) {
        if (!inWechat) {
          stateMessage("请复制当前链接到微信中打开，查看服务权益。", "请在微信中打开", null, "身份待验证");
          return;
        }
        stateMessage("微信授权未完成，请重新授权查看服务权益。", "重新授权", function () { authorize(false); }, "授权未完成");
        try {
          // The callback returns to this exact detail route on refusal or error.
          // Its one automatic attempt remains set until a trusted read succeeds.
          if (automatic && sessionStorage.getItem(authAttemptKey)) return;
          sessionStorage.setItem(authAttemptKey, "1");
        } catch (_) {
          // Without tab storage we cannot prove an automatic redirect is fresh.
          if (automatic) return;
        }
        stateMessage("正在前往微信授权，完成后自动返回当前详情。", "正在授权", null, "正在核验微信身份");
        window.location.assign(authURL);
      }

      function acceptTrustedState(state) {
        if (state.authenticated !== true) {
          authorize(true);
          return;
        }
        try { sessionStorage.removeItem(authAttemptKey); } catch (_) {}
        applyState(state);
      }

      async function refreshState(showLoading) {
        if (initialState.available === false) return;
        const generation = ++readGeneration;
        if (activeRead) activeRead.abort();
        const controller = new AbortController();
        activeRead = controller;
        if (showLoading) stateMessage("正在查询服务权益…", "正在查询", null, "正在查询服务权益");
        card.setAttribute("aria-busy", "true");
        // Match the existing public commerce request budget, not an OAuth retry.
        const deadline = setTimeout(function () { controller.abort(); }, 10000);
        try {
          const response = await fetch(window.location.pathname.replace(/^\/s\//, "/api/h5/service-period-products/") + window.location.search, {
            credentials: "same-origin", cache: "no-store", signal: controller.signal
          });
          if (!response.ok) throw new Error("state unavailable");
          const state = await response.json();
          if (!state || state.ok !== true || typeof state.authenticated !== "boolean" ||
              !state.entitlement || !["none", "active", "expired"].includes(state.entitlement.status)) {
            throw new Error("invalid state");
          }
          if (generation !== readGeneration) return;
          acceptTrustedState(state);
        } catch (_) {
          if (generation !== readGeneration) return;
          stateMessage("服务权益暂时无法查询，请重试。", "重新查询", function () { refreshState(true); }, "权益查询失败");
        } finally {
          clearTimeout(deadline);
          if (generation === readGeneration) {
            activeRead = null;
            card.setAttribute("aria-busy", "false");
          }
        }
      }

      function suspendRead() {
        ++readGeneration;
        if (activeRead) activeRead.abort();
        activeRead = null;
      }
      window.addEventListener("pagehide", suspendRead);
      window.addEventListener("pageshow", function (event) {
        if (event.persisted) refreshState(true);
      });
      document.addEventListener("visibilitychange", function () {
        if (document.visibilityState === "hidden") suspendRead();
        else refreshState(true);
      });

      if (initialState.available === false) {
        applyState(initialState);
      } else {
        if (initialState.authenticated === true && !initialState.read_failed) applyState(initialState);
        refreshState(initialState.authenticated !== true || initialState.read_failed === true);
      }
