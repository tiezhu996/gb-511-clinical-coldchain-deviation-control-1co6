
import { useEffect, useMemo, useState } from 'react';
import Button from '@mui/material/Button';
import AddOutlinedIcon from '@mui/icons-material/AddOutlined';
import PublishedWithChangesOutlinedIcon from '@mui/icons-material/PublishedWithChangesOutlined';
import { useTemperatureWindowStore } from '../stores/temperature-window';
import type { DomainRecord } from '../types/domain';
import { roleAtLeast } from '../types/domain';
import { getSession, ApiError } from '../api/client';
import { StatusBadge } from '../components/common/StatusBadge';
import { MetricCard } from '../components/common/MetricCard';
import { EvidenceList } from '../components/common/EvidenceList';
import { ConfirmDialog } from '../components/common/ConfirmDialog';
import { ActivationBlockPanel, ActivationImpactList } from '../components/common/ActivationBlockPanel';
import { formatDate } from '../utils/format';

export default function TemperatureWindowPage() {
  const store = useTemperatureWindowStore();
  const [pending, setPending] = useState<DomainRecord | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  // activeBlock is the immediate 422 response from the current submit attempt;
  // records with lastBlockReason carry the same reason on read-back after reload.
  const [activeBlock, setActiveBlock] = useState<DomainRecord | null>(null);
  const session = getSession();
  const canReview = roleAtLeast(session?.role, 'reviewer');
  useEffect(() => { void store.load('windows'); }, [store.load]);
  const active = useMemo(() => store.items.filter((item) => item.status === 'active').length, [store.items]);
  const critical = useMemo(() => store.items.filter((item) => item.riskLevel === 'critical').length, [store.items]);
  const blockedDrafts = useMemo(() => store.items.filter((item) => item.status === 'draft' && item.lastBlockReason), [store.items]);
  const latestBlock = useMemo(
    () => [...blockedDrafts].sort((a, b) => (b.lastBlockedAt || '').localeCompare(a.lastBlockedAt || ''))[0] || null,
    [blockedDrafts],
  );
  const createWindow = async () => {
    const suffix = Date.now().toString().slice(-5); const now = new Date().toISOString();
    await store.createRecord('windows', { code: `TW-UI-${suffix}`, name: '临床样本 2-8C 规则草案', description: '等待质量负责人复核生效', facility: '质量体系 QMS-UI', owner: session?.username || 'reviewer', category: '临床样本', riskLevel: 'high', metricValue: 8, metricUnit: 'C', effectiveAt: now, evidence: 'DRAFT-SOP-UI', relatedCode: 'DRAFT-SOP-UI', productClass: '临床样本', minimumCelsius: 2, maximumCelsius: 8, maxExcursionMinutes: 15, qualityOwner: session?.username || 'reviewer' });
    setCreateOpen(false);
  };
  const openActivation = (item: DomainRecord) => {
    setPending(item);
    setActiveBlock(item.lastBlockReason ? item : null);
  };
  const closeActivation = () => {
    setPending(null);
    setActiveBlock(null);
  };
  const activate = async () => {
    if (!pending) return;
    try {
      setActiveBlock(null);
      await store.transition('windows', pending, 'active', '质量负责人复核温控范围与允许时长', pending.evidence);
      closeActivation();
    } catch (error) {
      if (error instanceof ApiError && error.code === 'activation_blocked') {
        await store.load('windows');
        const reloaded = store.items.find((item) => item.id === pending.id) || pending;
        setActiveBlock(reloaded);
        setPending(reloaded);
        return;
      }
      throw error;
    }
  };
  return <main className="workspace"><header className="page-header"><div><p className="eyebrow">TEMPERATURE POLICY</p><h1>温控规则</h1><p>维护产品温度窗口与可接受偏差时长，规则生效需质量角色确认；生效前自动核验同产品类别、同场站的在途影响。</p></div>{canReview && <Button variant="contained" startIcon={<AddOutlinedIcon />} onClick={() => setCreateOpen(true)}>新增规则</Button>}</header>
    <section className="metrics"><MetricCard label="规则总数" value={store.meta.total} detail="版本化管理" /><MetricCard label="生效中" value={active} detail="参与偏差判断" /><MetricCard label="关键规则" value={critical} detail="深低温或高风险" /></section>
    {store.error && <div className="alert" role="alert">{store.error}</div>}
    {latestBlock && <ActivationBlockPanel code={latestBlock.code} reason={latestBlock.lastBlockReason} blockedAt={latestBlock.lastBlockedAt} impacts={latestBlock.lastBlockImpact || []} />}
    <section className="table-shell"><table><thead><tr><th>规则</th><th>产品类别</th><th>温度窗口</th><th>最长偏差</th><th>质量负责人</th><th>状态</th><th>依据</th><th>操作</th></tr></thead><tbody>{store.items.map((item) => <tr key={item.id} className={item.lastBlockReason && item.status === 'draft' ? 'row-blocked' : undefined}><td><strong>{item.code}</strong><small>{item.name}</small>{item.lastBlockReason && item.status === 'draft' && <small className="block-chip" title={item.lastBlockReason}>上次生效被阻断 · {item.lastBlockedAt ? formatDate(item.lastBlockedAt) : '时间未知'}</small>}</td><td>{item.productClass || item.category}</td><td><strong>{item.minimumCelsius ?? 0} 至 {item.maximumCelsius ?? item.metricValue} C</strong></td><td>{item.maxExcursionMinutes || 0} 分钟</td><td>{item.qualityOwner || item.owner}</td><td><StatusBadge status={item.status} /></td><td><EvidenceList evidence={item.evidence} /></td><td>{canReview && item.status === 'draft' ? <Button size="small" startIcon={<PublishedWithChangesOutlinedIcon />} onClick={() => openActivation(item)}>复核生效</Button> : <span className="muted">受控</span>}</td></tr>)}</tbody></table></section>
    <ConfirmDialog open={createOpen} title="创建温控规则草案" onCancel={() => setCreateOpen(false)} onConfirm={() => void createWindow()}><p>新规则默认保存为草稿，不会直接参与偏差判断。</p></ConfirmDialog>
    <ConfirmDialog open={Boolean(pending)} title={activeBlock ? '规则生效被阻断' : '确认规则生效'} onCancel={closeActivation} onConfirm={() => void activate()}>
      {activeBlock ? <>
        <p>草稿 <strong>{activeBlock.code}</strong> 未发生变更，旧规则继续生效。请处理以下影响后再次提交。</p>
        {activeBlock.lastBlockedAt && <p className="block-time">上次阻断提交：{formatDate(activeBlock.lastBlockedAt)}</p>}
        {activeBlock.lastBlockReason && <p className="block-reason">{activeBlock.lastBlockReason}</p>}
        <h4>影响清单（{activeBlock.lastBlockImpact?.length || 0}）</h4>
        <ActivationImpactList impacts={activeBlock.lastBlockImpact || []} />
        <p className="block-hint">影响处理完毕后再次点击“确认”提交；仍存在任一项时整次生效会继续失败。</p>
      </> : <p>请确认温度上下限、最长允许偏差时长和 SOP 依据已经复核。系统将先核验同产品类别、同场站是否存在仍在途容器或未结束偏差。</p>}
    </ConfirmDialog>
  </main>;
}
