
import LocalShippingOutlinedIcon from '@mui/icons-material/LocalShippingOutlined';
import AddAlertOutlinedIcon from '@mui/icons-material/AddAlertOutlined';
import VerifiedOutlinedIcon from '@mui/icons-material/VerifiedOutlined';
import type { ActivationImpact } from '../../types/domain';
import { formatDate } from '../../utils/format';

// ImpactCheck renders the pre-activation verification result: the product class + facility
// scope, the in-transit containers and unfinished excursions that block 生效, plus the
// persisted reason from the last refusal.
export function ImpactCheck({ impact, loading }: { impact: ActivationImpact | null; loading: boolean }) {
  if (loading) return <p className="impact-loading">正在核验同产品类别、同场站的在途容器与未结束偏差…</p>;
  if (!impact) return null;
  return <div className="impact-check">
    <p className="impact-scope">生效作用域：<strong>{impact.productClass || '未填写产品类别'}</strong> · <strong>{impact.facility || '未填写场站'}</strong></p>
    <section className="impact-group">
      <h4><LocalShippingOutlinedIcon /> 在途容器（{impact.containerCodes.length}）</h4>
      {impact.containerCodes.length
        ? <ul className="impact-list impact-list--danger">{impact.containerCodes.map((code) => <li key={code}>{code}</li>)}</ul>
        : <p className="impact-empty">无在途容器</p>}
    </section>
    <section className="impact-group">
      <h4><AddAlertOutlinedIcon /> 未结束偏差（{impact.excursionCodes.length}）</h4>
      {impact.excursionCodes.length
        ? <ul className="impact-list impact-list--danger">{impact.excursionCodes.map((code) => <li key={code}>{code}</li>)}</ul>
        : <p className="impact-empty">无 open / 复核中 / 已决定的偏差</p>}
    </section>
    {impact.blocked
      ? <p className="impact-blocked">存在未处理项，本次生效将被整次拒绝；草稿与旧规则均保持不变，处理完毕后再次提交才可生效。</p>
      : <p className="impact-clear"><VerifiedOutlinedIcon /> 影响核验通过，确认后草稿生效，同作用域旧规则同步转为「已替代」。</p>}
    {impact.lastBlockedReason && <p className="impact-history">上次阻断原因（{formatDate(impact.lastBlockedAt || '')}）：{impact.lastBlockedReason}</p>}
  </div>;
}
