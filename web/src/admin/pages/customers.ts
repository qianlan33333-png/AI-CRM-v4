import { listCustomers } from "../../api/generated/p3-contact/p3-contact";
import {
  type Customer as ApiCustomer,
  type CustomerListResponse,
  type ListCustomersParams,
} from "../../api/generated/health.schemas";
import { apiRequestOptions, unwrapGenerated } from "../../api/transport";
import {
  mount,
  PageBase,
  type StyleObj,
  type Vals,
} from "../../shared/ui/runtime";

const PAGE_SIZE = 50;

type Filters = { keyword: string; owner: string; mobile: string; tag: string };
type CustomerRow = {
  id: string;
  name: string;
  owner: string;
  mobileText: string;
  view: () => void;
};

type State = {
  filters: Filters;
  cursors: string[];
  page: number;
  loading: boolean;
  error: string;
};

class CustomersPage extends PageBase {
  override state: State = {
    filters: { keyword: "", owner: "", mobile: "", tag: "" },
    cursors: [],
    page: 0,
    loading: true,
    error: "",
  };

  private response: CustomerListResponse = {
    items: [],
    next_cursor: null,
    total: 0,
    total_is_estimate: false,
    watermark: "",
  };
  private requestSequence = 0;
  private abortController: AbortController | null = null;

  async init(): Promise<void> {
    await this.load(0, undefined, []);
  }

  private readFilters(): Filters {
    const value = (id: string): string =>
      (document.getElementById(id) as HTMLInputElement | null)?.value.trim() ||
      "";
    return {
      keyword: value("fCustomerKeyword"),
      owner: value("fCustomerOwner"),
      mobile: value("fCustomerMobile"),
      tag: value("fCustomerTag"),
    };
  }

  private params(filters: Filters, cursor?: string): ListCustomersParams {
    const params: ListCustomersParams = { limit: PAGE_SIZE };
    if (filters.keyword) params.keyword = filters.keyword;
    if (filters.mobile) {
      if (!/^1[3-9][0-9]{9}$/.test(filters.mobile)) {
        throw new Error("请输入11位中国大陆手机号");
      }
      params.mobile = filters.mobile;
    }
    if (filters.owner) {
      const owner = Number(filters.owner);
      if (!Number.isSafeInteger(owner) || owner < 1)
        throw new Error("负责人必须填写正整数 staff_id");
      params.owner_staff_id = owner;
    }
    if (filters.tag) {
      const tag = Number(filters.tag);
      if (!Number.isSafeInteger(tag) || tag < 1)
        throw new Error("标签必须填写正整数 tag_id");
      params.tag_id = tag;
    }
    if (cursor) params.cursor = cursor;
    return params;
  }

  private async load(
    page: number,
    cursor: string | undefined,
    cursors: string[],
    filters = this.state.filters,
  ): Promise<void> {
    let params: ListCustomersParams;
    try {
      params = this.params(filters, cursor);
    } catch (error) {
      this.setState({
        filters,
        error: error instanceof Error ? error.message : "筛选条件无效",
        loading: false,
      });
      return;
    }
    const sequence = ++this.requestSequence;
    this.abortController?.abort();
    const abortController = new AbortController();
    this.abortController = abortController;
    this.setState({ filters, loading: true, error: "" });
    try {
      const response = unwrapGenerated(
        await listCustomers(
          params,
          apiRequestOptions({ signal: abortController.signal }),
        ),
      ) as CustomerListResponse;
      if (sequence !== this.requestSequence) return;
      this.response = response;
      this.setState({ page, cursors, loading: false, error: "" });
    } catch (error) {
      if (sequence !== this.requestSequence || abortController.signal.aborted)
        return;
      this.response = {
        items: [],
        next_cursor: null,
        total: 0,
        total_is_estimate: false,
        watermark: "",
      };
      this.setState({
        loading: false,
        error: error instanceof Error ? error.message : "客户列表读取失败",
      });
    }
  }

  private query = (): void => {
    if (this.state.loading) return;
    const filters = this.readFilters();
    void this.load(0, undefined, [], filters);
  };

  private clear = (): void => {
    if (this.state.loading) return;
    void this.load(0, undefined, [], {
      keyword: "",
      owner: "",
      mobile: "",
      tag: "",
    });
  };

  private next = (): void => {
    if (this.state.loading || !this.response.next_cursor) return;
    const page = this.state.page + 1;
    const cursors = this.state.cursors.slice();
    cursors[page - 1] = this.response.next_cursor;
    void this.load(page, this.response.next_cursor, cursors);
  };

  private previous = (): void => {
    if (this.state.loading || this.state.page === 0) return;
    const page = this.state.page - 1;
    const cursor = page === 0 ? undefined : this.state.cursors[page - 1];
    void this.load(page, cursor, this.state.cursors.slice(0, page));
  };

  override renderVals(): Vals {
    const rows: CustomerRow[] = this.response.items.map(
      (customer: ApiCustomer) => ({
        id: String(customer.id),
        name: customer.name,
        owner:
          customer.owner_staff_id == null
            ? "未分配"
            : String(customer.owner_staff_id),
        mobileText: "当前契约未返回",
        view: () => {
          location.href = `customerDetail.html?id=${encodeURIComponent(String(customer.id))}`;
        },
      }),
    );
    const start = rows.length ? this.state.page * PAGE_SIZE + 1 : 0;
    const end = rows.length ? start + rows.length - 1 : 0;
    const estimate = this.response.total_is_estimate ? "（估算）" : "";
    const buttonStyle = (enabled: boolean): StyleObj => ({
      height: "28px",
      minWidth: "28px",
      padding: "0 8px",
      border: "1px solid #DEE0E3",
      borderRadius: "6px",
      background: "#fff",
      color: enabled ? "#1F2329" : "#BBBFC4",
      fontSize: "12px",
      cursor: enabled ? "pointer" : "not-allowed",
    });
    return {
      rows: { customers: rows },
      customersPage: {
        filters: this.state.filters,
        totalLabel: `共 ${this.response.total.toLocaleString()} 位客户${estimate}`,
        rangeLabel: this.state.loading
          ? "正在读取客户列表…"
          : rows.length
            ? `第 ${start} – ${end} 条，共 ${this.response.total.toLocaleString()} 条${estimate}`
            : `暂无客户，共 ${this.response.total.toLocaleString()} 条${estimate}`,
        previous: this.previous,
        next: this.next,
        query: this.query,
        clear: this.clear,
        page: this.state.page + 1,
        previousStyle: buttonStyle(this.state.page > 0 && !this.state.loading),
        nextStyle: buttonStyle(
          Boolean(this.response.next_cursor) && !this.state.loading,
        ),
        error: this.state.error,
        empty: rows.length === 0 && !this.state.loading && !this.state.error,
      },
    };
  }
}

function boot(): void {
  const stage = document.getElementById("stage");
  const template = document.getElementById("tpl") as HTMLTemplateElement | null;
  if (!stage || !template) return;
  const page = new CustomersPage();
  mount(stage, template.innerHTML, page);
  void page.init();
}

if (document.readyState === "loading")
  document.addEventListener("DOMContentLoaded", boot, { once: true });
else boot();
