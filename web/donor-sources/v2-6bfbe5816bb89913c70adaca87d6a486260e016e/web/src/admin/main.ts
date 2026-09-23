/**
 * 管理后台轻量入口。这里只识别页面并加载对应领域模块，禁止静态引用页面实现。
 */
const page = document.body.dataset.page || "customers";
const query = new URLSearchParams(location.search);

const loader =
  page === "customers" && query.get("message_history") !== "1"
    ? import("./pages/customers")
    : import("./legacy");

void loader.catch((error: unknown) => {
  const stage = document.getElementById("stage");
  if (!stage) return;
  stage.innerHTML = `<div role="alert" style="margin:32px;padding:24px;border:1px solid #F2B8B5;border-radius:8px;color:#D83931;background:#FFF1F0">${error instanceof Error ? error.message : "页面模块加载失败"}</div>`;
});
