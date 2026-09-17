
import { BehaviorSubject } from 'rxjs';
import { ApiError, request } from '../api/client';
import { fetchReviewCheck } from '../api/compliance-decision';
import type { DomainRecord, PageMeta, ReviewCheck } from '../types/domain';

export interface EntityState {
  items: DomainRecord[];
  meta: PageMeta;
  loading: boolean;
  error: string;
  // decisionId -> read-only gate verdict (decisions page only)
  reviewChecks: Record<number, ReviewCheck>;
  // structured verdict returned by the last blocked transition (decisions page only)
  gateError: ReviewCheck | null;
}
const initialState: EntityState = { items: [], meta: { page: 1, pageSize: 20, total: 0 }, loading: false, error: '', reviewChecks: {}, gateError: null };

export class EntityStore {
  private readonly subject = new BehaviorSubject<EntityState>(initialState);
  readonly state$ = this.subject.asObservable();
  get snapshot() { return this.subject.value; }

  async load(path: string, search = '') {
    this.patch({ loading: true, error: '', gateError: null });
    try {
      const result = await request<DomainRecord[]>(`/${path}?page=1&pageSize=20&search=${encodeURIComponent(search)}`);
      const items = result.data;
      this.subject.next({ items, meta: result.meta || { page: 1, pageSize: 20, total: items.length }, loading: false, error: '', reviewChecks: {}, gateError: null });
      if (path === 'decisions') await this.loadReviewChecks(items);
    } catch (error) {
      this.patch({ loading: false, error: error instanceof Error ? error.message : String(error) });
    }
  }

  // Re-reads the gate verdict for decisions that can still enter review or a
  // final judgement. Failures are tolerated per row: a missing preview must not
  // block the list, while the backend transition still enforces the gate.
  async loadReviewChecks(items: DomainRecord[]) {
    const actionable = items.filter((item) => item.status === 'draft' || item.status === 'review');
    if (!actionable.length) return;
    const settled = await Promise.allSettled(actionable.map((item) => fetchReviewCheck(item.id)));
    const reviewChecks: Record<number, ReviewCheck> = {};
    settled.forEach((outcome, index) => {
      if (outcome.status === 'fulfilled' && outcome.value.data) reviewChecks[actionable[index].id] = outcome.value.data;
    });
    this.patch({ reviewChecks: { ...this.subject.value.reviewChecks, ...reviewChecks } });
  }

  async createRecord(path: string, input: Partial<DomainRecord>) {
    this.patch({ loading: true, gateError: null });
    try {
      await request<DomainRecord>(`/${path}`, { method: 'POST', body: JSON.stringify(input) });
      await this.load(path);
    } catch (error) {
      this.patch({ loading: false, error: this.messageOf(error) });
      throw error;
    }
  }

  async transition(path: string, item: DomainRecord, status: string) {
    this.patch({ loading: true, error: '', gateError: null });
    try {
      await request<DomainRecord>(`/${path}/${item.id}/transition`, {
        method: 'POST', body: JSON.stringify({ status, expectedVersion: item.version, reason: '前端工作台人工确认' }),
      });
      await this.load(path);
    } catch (error) {
      // Preserve the original-state reasons for the page; no optimistic update
      // is ever applied, so the row keeps its prior status and version.
      const gateError = error instanceof ApiError ? error.reviewCheck ?? null : null;
      this.patch({ loading: false, error: this.messageOf(error), gateError });
      if (path === 'decisions') await this.loadReviewChecks(this.subject.value.items);
      throw error;
    }
  }

  dismissGate() { this.patch({ gateError: null, error: '' }); }

  private patch(value: Partial<EntityState>) { this.subject.next({ ...this.subject.value, ...value }); }
  private messageOf(error: unknown) { return error instanceof Error ? error.message : String(error); }
}
