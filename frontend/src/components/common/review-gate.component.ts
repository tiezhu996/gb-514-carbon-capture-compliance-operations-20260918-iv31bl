import { CommonModule } from '@angular/common';
import { Component, Input } from '@angular/core';
import type { ReviewGate } from '../../types/domain';

@Component({
  selector: 'app-review-gate',
  standalone: true,
  imports: [CommonModule],
  template: `<div class="gate gate--{{ gate.outcome }}" *ngIf="gate" [class.gate--inline]="inline">
    <div class="gate__head">
      <span class="gate__badge">{{ label(gate.outcome) }}</span>
      <small *ngIf="gate.allowedAction">唯一允许操作 · {{ actionLabel(gate.allowedAction) }}</small>
      <small *ngIf="!gate.allowedAction && gate.outcome === 'ready'">可由复核人接受</small>
    </div>
    <dl class="gate__snapshot" *ngIf="gate.ruleCode || gate.sampleCode">
      <div><dt>生效规则</dt><dd>{{ gate.ruleCode || '—' }}<em *ngIf="gate.ruleVersion"> v{{ gate.ruleVersion }}</em></dd></div>
      <div><dt>许可阈值</dt><dd>{{ gate.ruleThreshold ?? '—' }} {{ gate.metricUnit || '' }}</dd></div>
      <div><dt>最新已核验样本</dt><dd>{{ gate.sampleCode || '—' }}<em *ngIf="gate.sampleReading !== undefined"> · {{ gate.sampleReading }} {{ gate.metricUnit || '' }}</em></dd></div>
    </dl>
    <ul class="gate__reasons" *ngIf="gate.reasons?.length">
      <li *ngFor="let reason of gate.reasons">{{ reason }}</li>
    </ul>
  </div>`,
})
export class ReviewGateComponent {
  @Input({ required: true }) gate!: ReviewGate;
  @Input() inline = false;

  label(outcome: ReviewGate['outcome']): string {
    if (outcome === 'ready') return '阈值内 · 可接受';
    if (outcome === 'exceeded') return '超阈值 · 仅可升级';
    return '证据受阻 · 保留原状态';
  }

  actionLabel(action: string): string {
    if (action === 'accepted') return '接受';
    if (action === 'escalated') return '升级';
    return action;
  }
}
