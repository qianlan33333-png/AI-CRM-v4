/** The contact projection is separate from the identity phone/bind action. */
export function mountSidebarContacts(
  profile: Record<string, any>,
  customerID: string,
  checkContext: () => void,
): void {
  document.getElementById("sidebar-contact-profile")?.remove();
  const host = document.querySelector("#sidebar-workbench-root .profile-card");
  if (
    !host ||
    (!profile.contact_phone_masked && !profile.contact_shipping_summary)
  )
    return;
  const block = document.createElement("div");
  block.id = "sidebar-contact-profile";
  block.className = "contact-profile";
  const phone = document.createElement("div");
  phone.className = "meta";
  const address = document.createElement("div");
  address.className = "meta";
  const masked = () => {
    phone.textContent = profile.contact_phone_masked
      ? `联系电话 ${profile.contact_phone_masked}`
      : "";
    address.textContent = profile.contact_shipping_summary
      ? `收货信息 ${profile.contact_shipping_summary}`
      : "";
  };
  masked();
  block.append(phone, address);
  host.append(block);
  // Reveal retains the existing administrator + CSRF boundary. WeCom-only
  // staff sessions can see the safe projection but never obtain plaintext.
  void (async () => {
    try {
      const response = await fetch("/api/admin/access/cli/session", {
        credentials: "same-origin",
        cache: "no-store",
        redirect: "error",
      });
      if (!response.ok) return;
      const session = await response.json();
      checkContext();
      if (!block.isConnected) return;
      if (
        !Array.isArray(session.roles) ||
        !session.roles.some((r: string) => r === "admin" || r === "super_admin")
      )
        return;
      const button = document.createElement("button");
      button.type = "button";
      button.className = "contact-reveal";
      button.textContent = "查看完整资料";
      block.append(button);
      let revealed = false;
      button.addEventListener("click", async () => {
        if (revealed) {
          masked();
          revealed = false;
          button.textContent = "查看完整资料";
          return;
        }
        button.disabled = true;
        try {
          checkContext();
          const csrf =
            document.cookie
              .split(";")
              .map((v) => v.trim())
              .find((v) => v.startsWith("aicrm_admin_csrf="))
              ?.slice("aicrm_admin_csrf=".length) || "";
          const result = await fetch(
            `/api/admin/customers/${encodeURIComponent(customerID)}/contact-reveal`,
            {
              method: "POST",
              credentials: "same-origin",
              cache: "no-store",
              redirect: "error",
              headers: { "X-CSRF-Token": decodeURIComponent(csrf) },
            },
          );
          checkContext();
          if (!block.isConnected) return;
          if (!result.ok) throw new Error("没有查看权限或资料暂不可用");
          const value = await result.json();
          checkContext();
          if (!block.isConnected) return;
          phone.textContent = value.mobile_e164
            ? `联系电话 ${String(value.mobile_e164).replace(/^\+86/, "")}`
            : "";
          const a = value.shipping_address;
          address.textContent = a
            ? `收货信息 ${a.recipient_name} ${a.province_name}${a.city_name}${a.district_name} ${a.detail_address}`
            : "";
          revealed = true;
          button.textContent = "收起";
        } catch {
          if (block.isConnected) {
            masked();
            button.textContent = "查看失败，重试";
          }
        } finally {
          button.disabled = false;
        }
      });
    } catch {
      /* No administrator session: retain the masked projection. */
    }
  })();
}
