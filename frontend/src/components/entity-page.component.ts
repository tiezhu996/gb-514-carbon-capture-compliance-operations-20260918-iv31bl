import { AsyncPipe, CommonModule } from '@angular/common';
import { ChangeDetectorRef, Component, Input, OnInit } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatInputModule } from '@angular/material/input';
import { authState } from '../hooks/use-auth';
import type { EntityStore } from '../stores/factory';
import type { DomainRecord, EntityConfig, ReviewGate } from '../types/domain';
import { formatDate, nextStatus } from '../utils/format';
import { ConfirmDialogComponent } from './common/confirm-dialog.component';
import { ComplianceBadgeComponent } from './common/compliance-badge.component';
import { EvidenceListComponent } from './common/evidence-list.component';
import { MetricCardComponent } from './common/metric-card.component';
import { ReviewGateComponent } from './common/review-gate.component';
import { RuleDiffComponent } from './common/rule-diff.component';
import { StatusBadgeComponent } from './common/status-badge.component';

interface DecisionAction { target: string; label: string; reviewerOnly: boolean }

@Component({
  selector: 'app-entity-page',
  standalone: true,
  imports: [CommonModule, AsyncPipe, FormsModule, MatButtonModule, MatInputModule, StatusBadgeComponent, MetricCardComponent, ConfirmDialogComponent, EvidenceListComponent, ComplianceBadgeComponent, RuleDiffComponent, ReviewGateComponent],
  template: `<main class="workspace" *ngIf="store.state$ | async as state">
    <header class="page-header">
      <div><p class="eyebrow">业务工作台</p><h1>{{ config.label }}</h1><p>统一管理{{ config.label }}的状态、风险、证据与责任人。</p></div>
      <button *ngIf="canWrite()" mat-flat-button color="primary" (click)="openCreate()">新增{{ config.label }}</button>
    </header>
    <section class="metrics">
      <app-metric-card label="记录总数" [value]="state.meta.total" detail="当前筛选范围"/>
      <app-metric-card label="高风险" [value]="highRisk(state.items)" detail="需要优先复核"/>
      <app-metric-card label="状态种类" [value]="statusCount(state.items)" detail="状态机覆盖"/>
    </section>
    <app-compliance-badge *ngIf="config.path === 'units' || config.path === 'decisions'" [records]="state.items"/>
    <app-rule-diff *ngIf="config.path === 'rules' || config.path === 'samples'" [records]="state.items"/>
    <app-evidence-list [records]="state.items"/>
    <section class="gate-panel" *ngIf="isDecisions()">
      <header><strong>复核闭环核验</strong><span>进入复核与最终判定时按关联装置重读当前生效许可规则与最新已核验样本</span></header>
      <div class="gate-grid">
        <app-review-gate *ngFor="let item of actionable(state.items)" [gate]="item.reviewGate!" />
        <div class="empty" *ngIf="!actionable(state.items).length">暂无可核验的复核中决定</div>
      </div>
    </section>
    <section class="toolbar">
      <input matInput aria-label="搜索" [(ngModel)]="search" [placeholder]="'搜索' + config.label + '编码或名称'"/>
      <button mat-flat-button color="primary" (click)="query()">查询</button>
      <button mat-button (click)="reset()">重置</button>
    </section>
    <div *ngIf="state.error" class="alert">{{ state.error }}</div>
    <section class="table-shell"><table><thead><tr><th>编码</th><th>名称</th><th>状态</th><th *ngIf="isDecisions()">复核核验</th><th>风险</th><th>责任人</th><th>指标</th><th>更新时间</th><th>操作</th></tr></thead>
      <tbody><tr *ngFor="let item of state.items"><td><strong>{{ item.code }}</strong></td><td>{{ item.name }}<small>{{ item.facility }}</small></td><td><app-status-badge [status]="item.status"/></td>
        <td class="cell-gate" *ngIf="isDecisions()">
          <ng-container *ngIf="item.reviewGate as gate; else noGate">
            <app-review-gate [gate]="gate" [inline]="true"/>
          </ng-container>
          <ng-template #noGate><span class="muted">未关联装置，无法核验</span></ng-template>
        </td>
        <td>{{ item.riskLevel }}</td><td>{{ item.owner }}</td><td>{{ item.metricValue }} {{ item.metricUnit }}</td><td>{{ formatDate(item.updatedAt) }}</td><td>
        <ng-container *ngIf="decisionAction(item) as action; else linearAction">
          <button *ngIf="canRunAction(action)" class="table-action" (click)="openTransition(item, action.target, action.label)">{{ action.label }}</button>
          <span *ngIf="!canRunAction(action)" class="muted">{{ actionHintForDecision(action) }}</span>
        </ng-container>
        <ng-template #linearAction>
          <button *ngIf="canTransition(item)" class="table-action" (click)="openTransition(item, next(item)!, '推进至 ' + next(item)!)">推进至 {{ next(item) }}</button>
          <span *ngIf="!canTransition(item)" class="muted">{{ actionHint(item) }}</span>
        </ng-template>
      </td></tr><tr *ngIf="!state.items.length && !state.loading"><td [colSpan]="isDecisions() ? 9 : 8" class="empty">暂无记录</td></tr></tbody></table>
      <div *ngIf="state.loading" class="loading">正在同步业务数据…</div>
    </section>
    <app-confirm-dialog [open]="showCreate" [title]="'新增' + config.label" (cancel)="closeCreate()" (confirm)="createDemo()"><p>将创建一条包含完整责任人、风险和证据信息的演示记录。</p></app-confirm-dialog>
    <app-confirm-dialog [open]="!!pending" title="确认状态迁移" (cancel)="closeTransition()" (confirm)="confirmTransition()"><p>状态迁移会追加不可变版本并记录证据、操作者与请求 ID；复核核验未通过时保留原状态。</p><strong>{{ pending?.item?.status }} → {{ pending?.label }}</strong></app-confirm-dialog>
  </main>`,
})
export class EntityPageComponent implements OnInit {
  @Input({ required: true }) config!: EntityConfig;
  @Input({ required: true }) store!: EntityStore;
  search = '';
  showCreate = false;
  pending: { item: DomainRecord; status: string; label: string } | null = null;
  readonly formatDate = formatDate;

  constructor(private readonly changeDetector: ChangeDetectorRef) {}
  async ngOnInit() { await this.load(); }
  isDecisions() { return this.config.path === 'decisions'; }
  next(item: DomainRecord) { return nextStatus(item.status, this.config.statuses); }
  canWrite() { return authState.hasMinimumRole('operator'); }
  canReview() { return authState.hasMinimumRole('reviewer'); }

  // 非决定实体沿用线性状态流。
  canTransition(item: DomainRecord): boolean {
    const target = this.next(item);
    return !!target && this.canWrite();
  }
  actionHint(item: DomainRecord): string {
    if (!this.canWrite()) return '只读权限';
    return '流程结束';
  }

  // 复核闭环：操作按钮完全由“当前状态 + 重读后的 gate 结论”决定。
  decisionAction(item: DomainRecord): DecisionAction | null {
    if (!this.isDecisions()) return null;
    const gate = item.reviewGate ?? null;
    switch (item.status) {
      case 'draft':
        if (gate && gate.outcome === 'blocked') return null;
        return { target: 'review', label: '提交复核', reviewerOnly: false };
      case 'review':
        if (!gate) return null;
        if (gate.outcome === 'ready') return { target: 'accepted', label: '接受决定', reviewerOnly: true };
        if (gate.outcome === 'exceeded') return { target: 'escalated', label: '升级处理', reviewerOnly: true };
        return { target: 'draft', label: '退回补证', reviewerOnly: false };
      case 'accepted':
        if (gate && gate.outcome === 'exceeded') return { target: 'escalated', label: '升级处理', reviewerOnly: true };
        return null;
      case 'escalated':
        if (gate && gate.outcome === 'ready') return { target: 'accepted', label: '接受决定', reviewerOnly: true };
        return null;
      default:
        return null;
    }
  }
  canRunAction(action: DecisionAction): boolean {
    return action.reviewerOnly ? this.canReview() : this.canWrite();
  }
  actionHintForDecision(action: DecisionAction): string {
    if (!this.canWrite()) return '只读权限';
    return action.reviewerOnly ? '等待复核员决定' : '流程结束';
  }
  actionable(items: DomainRecord[]): DomainRecord[] {
    return items.filter((item) => !!item.reviewGate && (item.status === 'review' || item.status === 'accepted' || item.status === 'escalated'));
  }
  highRisk(items: DomainRecord[]) { return items.filter((item) => ['high', 'critical'].includes(item.riskLevel)).length; }
  statusCount(items: DomainRecord[]) { return new Set(items.map((item) => item.status)).size; }
  async query() { await this.load(this.search); }
  async reset() { this.search = ''; await this.load(); }
  openCreate() { if (this.canWrite()) this.showCreate = true; this.changeDetector.detectChanges(); }
  closeCreate() { this.showCreate = false; this.changeDetector.detectChanges(); }
  openTransition(item: DomainRecord, status: string, label: string) {
    const action = this.decisionAction(item);
    if (this.isDecisions()) {
      if (!action || action.target !== status || !this.canRunAction(action)) return;
    } else if (!this.canTransition(item)) {
      return;
    }
    this.pending = { item, status, label };
    this.changeDetector.detectChanges();
  }
  closeTransition() { this.pending = null; this.changeDetector.detectChanges(); }
  async createDemo() {
    if (!this.canWrite()) return;
    const now = Date.now();
    try {
      await this.store.createRecord(this.config.path, {
        code: `${this.config.key.toUpperCase()}-${String(now).slice(-6)}`, name: `新增${this.config.label}`,
        description: '通过前端工作台创建的业务记录', facility: '默认作业区', owner: '现场操作员',
        category: '常规', riskLevel: 'medium', metricValue: 25, metricUnit: 'unit',
        effectiveAt: new Date().toISOString(), evidence: '已完成创建前检查', relatedCode: '',
      });
      this.search = '';
      this.showCreate = false;
    } catch { /* Store exposes the request error in its observable state. */ }
    finally { this.changeDetector.detectChanges(); }
  }
  async confirmTransition() {
    if (!this.pending) return;
    const { item, status } = this.pending;
    const action = this.decisionAction(item);
    if (this.isDecisions()) {
      if (!action || action.target !== status || !this.canRunAction(action)) return;
    } else if (!this.canTransition(item)) {
      return;
    }
    try {
      await this.store.transition(this.config.path, item, status);
      this.search = '';
      this.pending = null;
    } catch { /* Store exposes the request error in its observable state. */ }
    finally { this.changeDetector.detectChanges(); }
  }
  private async load(search = '') { await this.store.load(this.config.path, search); this.changeDetector.detectChanges(); }
}
