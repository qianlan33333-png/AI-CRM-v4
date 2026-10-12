import type {
  SurveyPublicShippingContact,
  SurveyPublicRegion,
} from "../../generated/survey-public";

export type ShippingContact = SurveyPublicShippingContact;
export type Region = SurveyPublicRegion;
export const emptyShippingContact = (): ShippingContact => ({
  mobile_e164: "",
  recipient_name: "",
  province_code: "",
  province_name: "",
  city_code: "",
  city_name: "",
  district_code: "",
  district_name: "",
  detail_address: "",
});
export function shippingContactError(
  value: ShippingContact | undefined,
  required: boolean,
): string {
  if (!value || !Object.values(value).some(Boolean))
    return required ? "请填写收货信息" : "";
  if (!value.recipient_name.trim()) return "请填写收件人";
  if (!/^(?:\+86)?1[3-9][0-9]{9}$/.test(value.mobile_e164))
    return "请输入正确的大陆 11 位手机号";
  if (!value.province_code || !value.city_code || !value.district_code)
    return "请选择完整所在地区";
  if (!value.detail_address.trim()) return "请填写详细地址";
  return "";
}

// Used by both questionnaire display modes. Native inputs/selects preserve the
// established form behavior; callers own submission, authorization and storage.
export function mergeShippingPrefill(
  current: ShippingContact,
  saved: ShippingContact,
  touched: Set<string>,
): ShippingContact {
  const next = { ...current };
  for (const key of [
    "recipient_name",
    "mobile_e164",
    "detail_address",
  ] as const)
    if (!touched.has(key) && !next[key]) next[key] = saved[key] || "";
  if (
    !["province_code", "city_code", "district_code"].some(
      (key) => touched.has(key) || current[key as keyof ShippingContact],
    )
  ) {
    for (const key of [
      "province_code",
      "province_name",
      "city_code",
      "city_name",
      "district_code",
      "district_name",
    ] as const)
      next[key] = saved[key] || "";
  }
  return next;
}
export type ShippingContactForm = {
  read: () => ShippingContact;
  prefill: (value: ShippingContact) => ShippingContact;
};
export function mountShippingContact(
  container: HTMLElement,
  initial: ShippingContact,
  regions: Region[],
  changed: (value: ShippingContact) => void,
  touched = new Set<string>(),
): ShippingContactForm {
  const fields = new Map<string, HTMLInputElement | HTMLSelectElement>();
  container.replaceChildren();
  const row = (label: string): HTMLLabelElement => {
    const item = document.createElement("label");
    item.style.cssText =
      "display:flex;align-items:center;gap:12px;min-height:52px;border-bottom:1px solid #e6e8ec;font-size:15px;";
    const title = document.createElement("span");
    title.textContent = label;
    title.style.cssText = "flex:0 0 72px;color:#20242b";
    item.append(title);
    container.append(item);
    return item;
  };
  const input = (
    key: string,
    label: string,
    placeholder: string,
    autocomplete: string,
    type = "text",
  ) => {
    const field = document.createElement("input");
    field.type = type;
    field.autocomplete = autocomplete as AutoFill;
    field.placeholder = placeholder;
    field.setAttribute("aria-label", label);
    field.dataset.contactField = key;
    field.style.cssText =
      "min-width:0;flex:1;width:100%;height:44px;border:0;background:transparent;font:inherit;outline-offset:2px";
    field.value = String(initial[key as keyof ShippingContact] || "").replace(
      key === "mobile_e164" ? /^\+86/ : /^$/,
      "",
    );
    if (type === "tel") field.inputMode = "tel";
    row(label).append(field);
    fields.set(key, field);
    return field;
  };
  input("recipient_name", "收件人", "请输入收件人", "name");
  input("mobile_e164", "手机号", "请输入手机号", "tel-national", "tel");
  const regionRow = row("所在地区");
  const selects = document.createElement("span");
  selects.style.cssText = "display:flex;flex:1;min-width:0;gap:4px";
  regionRow.append(selects);
  for (const [key, label] of [
    ["province", "省"],
    ["city", "市"],
    ["district", "区/县"],
  ]) {
    const field = document.createElement("select");
    field.setAttribute("aria-label", label);
    field.dataset.contactField = key + "_code";
    field.style.cssText =
      "min-width:0;width:33.33%;height:44px;border:0;background:transparent;font:inherit";
    selects.append(field);
    fields.set(key + "_code", field);
  }
  input("detail_address", "详细地址", "街道、门牌号等", "street-address");
  const select = (key: string) =>
    fields.get(key + "_code") as HTMLSelectElement;
  const fill = (key: string, items: Region[], value: string) => {
    const field = select(key);
    field.replaceChildren(
      new Option(
        { province: "省", city: "市", district: "区/县" }[key] || "",
        "",
      ),
    );
    for (const option of items)
      field.add(new Option(option.n, String(option.c)));
    field.value = value;
    field.disabled = items.length === 0;
  };
  const province = () =>
    regions.find((item) => String(item.c) === select("province").value);
  const city = () =>
    province()?.ch?.find((item) => String(item.c) === select("city").value);
  fill("province", regions, initial.province_code);
  fill("city", province()?.ch || [], initial.city_code);
  fill("district", city()?.ch || [], initial.district_code);
  const read = () => {
    const value = emptyShippingContact();
    for (const [key, field] of fields)
      value[key as keyof ShippingContact] = field.value.trim();
    for (const key of ["province", "city", "district"])
      value[(key + "_name") as keyof ShippingContact] = select(key).value
        ? select(key).selectedOptions[0]?.textContent || ""
        : "";
    if (value.mobile_e164 && !value.mobile_e164.startsWith("+86"))
      value.mobile_e164 = "+86" + value.mobile_e164;
    return value;
  };
  for (const [key, field] of fields)
    field.addEventListener(
      field.tagName === "SELECT" ? "change" : "input",
      () => {
        touched.add(key);
        if (key === "province_code") {
          fill("city", province()?.ch || [], "");
          fill("district", [], "");
        }
        if (key === "city_code") fill("district", city()?.ch || [], "");
        changed(read());
      },
    );
  const prefill = (saved: ShippingContact) => {
    const next = mergeShippingPrefill(read(), saved, touched);
    for (const key of ["recipient_name", "mobile_e164", "detail_address"]) {
      const field = fields.get(key)!;
      if (!touched.has(key) && !field.value)
        field.value = String(next[key as keyof ShippingContact]).replace(
          key === "mobile_e164" ? /^\+86/ : /^$/,
          "",
        );
    }
    if (
      !["province_code", "city_code", "district_code"].some(
        (key) => touched.has(key) || fields.get(key)!.value,
      )
    ) {
      fill("province", regions, next.province_code);
      fill("city", province()?.ch || [], next.city_code);
      fill("district", city()?.ch || [], next.district_code);
    }
    return read();
  };
  return { read, prefill };
}
