
import BlockOutlinedIcon from '@mui/icons-material/BlockOutlined';
import LocalShippingOutlinedIcon from '@mui/icons-material/LocalShippingOutlined';
import WarningAmberOutlinedIcon from '@mui/icons-material/WarningAmberOutlined';
import type { ActivationImpact } from '../../types/domain';
import { StatusBadge } from './StatusBadge';
import { formatDate } from '../../utils/format';

export function impactTypeLabel(type: string): string {
  return type === 'container' ? '仍在途容器' : '未结束偏差';
}

export function ActivationImpactList({ impacts }: { impacts: ActivationImpact[] }) {
  if (!impacts.length) return <p className="impact-empty">未返回影响编号，请刷新后重试。</p>;
  return <ul className="impact-list" aria-label="生效影响清单">
    {impacts.map((impact) => (
      <li key={`${impact.type}-${impact.code}`} className={`impact-item impact-item--${impact.type}`}>
        <span className="impact-icon" aria-hidden="true">
          {impact.type === 'container' ? <LocalShippingOutlinedIcon /> : <WarningAmberOutlinedIcon />}
        </span>
        <span className="impact-body">
          <strong>{impactTypeLabel(impact.type)} · {impact.code}</strong>
          <small>{impact.name}</small>
        </span>
        <StatusBadge status={impact.status} />
      </li>
    ))}
  </ul>;
}

export function ActivationBlockPanel({ code, reason, blockedAt, impacts }: { code: string; reason?: string; blockedAt?: string; impacts: ActivationImpact[] }) {
  return <section className="block-panel" role="alert">
    <header>
      <BlockOutlinedIcon aria-hidden="true" />
      <div>
        <strong>规则生效被阻断 · {code}</strong>
        {blockedAt && <small>上次阻断提交：{formatDate(blockedAt)}</small>}
      </div>
    </header>
    {reason && <p className="block-reason">{reason}</p>}
    <p className="block-hint">请先完成同产品类别和场站的在途容器签收/放行，并结束关联偏差；处理完毕后再次提交生效，草稿与旧规则在此之前均保持不变。</p>
    <h4>影响清单（{impacts.length}）</h4>
    <ActivationImpactList impacts={impacts} />
  </section>;
}
