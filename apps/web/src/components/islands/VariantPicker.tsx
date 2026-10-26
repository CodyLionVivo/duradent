// Interactive variant selection for the product page. The server renders it
// with the initial variant; on the client it reacts to option changes and keeps
// ?variant= in the URL so a selection can be shared.
import { useMemo, useState } from 'preact/hooks';

export interface PickerOption {
  code: string;
  name: string;
  values: { code: string; label: string }[];
}

export interface PickerVariant {
  id: string;
  sku: string;
  weight_g: number;
  options: Record<string, string>;
}

export interface PickerQuote {
  unit_price: string;
  tiers: { min_qty: number; unit_price: string }[];
}

export interface PickerLabels {
  chooseOption: string; // contains {option}
  sku: string;
  weight: string; // contains {value}
  unitPrice: string;
  volumePricing: string;
  fromQty: string; // contains {qty}
  perUnit: string;
  taxNote: string;
  priceOnRequest: string;
  unavailable: string;
  addToCart: string;
  quantity: string;
  inStock: string; // contains {count}
  outOfStock: string;
}

interface Props {
  options: PickerOption[];
  variants: PickerVariant[];
  quotes: Record<string, PickerQuote>;
  availability: Record<string, number>;
  /** Form target that adds to the cart (works without JavaScript too). */
  cartAction: string;
  initialVariantId: string;
  currency: string;
  intlLocale: string;
  labels: PickerLabels;
}

const fill = (template: string, key: string, value: string | number) => template.replace(`{${key}}`, String(value));

function matches(v: PickerVariant, selection: Record<string, string>): boolean {
  return Object.entries(selection).every(([k, val]) => v.options[k] === val);
}

export default function VariantPicker({ options, variants, quotes, availability, cartAction, initialVariantId, currency, intlLocale, labels }: Props) {
  const initial = variants.find((v) => v.id === initialVariantId) ?? variants[0];
  const [selection, setSelection] = useState<Record<string, string>>({ ...(initial?.options ?? {}) });
  const [qty, setQty] = useState(1);

  const money = useMemo(
    () => new Intl.NumberFormat(intlLocale, { style: 'currency', currency, minimumFractionDigits: 2 }),
    [intlLocale, currency],
  );
  const current = variants.find((v) => matches(v, selection) && Object.keys(v.options).length === Object.keys(selection).length);
  const quote = current ? quotes[current.id] : undefined;
  const stock = current ? (availability[current.id] ?? 0) : 0;
  const canBuy = !!current && !!quote && stock > 0;

  const unitFor = (q: PickerQuote, n: number) =>
    [...q.tiers].reverse().find((t) => t.min_qty <= n)?.unit_price ?? q.unit_price;

  function choose(code: string, value: string) {
    const wanted = { ...selection, [code]: value };
    // Keep the other choices when that combination exists; otherwise jump to
    // the first variant with the chosen value so the picker never dead-ends.
    const next = variants.find((v) => matches(v, wanted)) ?? variants.find((v) => v.options[code] === value);
    if (!next) return;
    setSelection({ ...next.options });
    const url = new URL(window.location.href);
    url.searchParams.set('variant', next.id);
    window.history.replaceState(null, '', url);
  }

  const available = (code: string, value: string) =>
    variants.some((v) => v.options[code] === value && Object.entries(selection).every(([k, val]) => k === code || v.options[k] === val));

  return (
    <div class="space-y-6">
      {options.map((opt) => (
        <fieldset key={opt.code}>
          <legend class="mb-2 text-sm font-medium text-ink">
            {fill(labels.chooseOption, 'option', opt.name.toLowerCase())}
          </legend>
          <div class="flex flex-wrap gap-2">
            {opt.values.map((val) => {
              const checked = selection[opt.code] === val.code;
              const possible = available(opt.code, val.code);
              return (
                <label
                  key={val.code}
                  class={[
                    'cursor-pointer rounded-lg border px-3.5 py-2 text-sm transition-colors has-[:focus-visible]:outline has-[:focus-visible]:outline-3 has-[:focus-visible]:outline-brand-500',
                    checked
                      ? 'border-brand-700 bg-brand-50 font-semibold text-brand-800'
                      : possible
                        ? 'border-line bg-white text-ink hover:border-brand-500'
                        : 'border-dashed border-line bg-white text-muted',
                  ].join(' ')}
                >
                  <input
                    type="radio"
                    class="sr-only"
                    name={`opt-${opt.code}`}
                    value={val.code}
                    checked={checked}
                    onChange={() => choose(opt.code, val.code)}
                  />
                  {val.label}
                </label>
              );
            })}
          </div>
        </fieldset>
      ))}

      <div class="rounded-xl border border-line bg-surface p-5" aria-live="polite">
        {current ? (
          <>
            <p class="text-xs text-muted">
              {labels.sku}: <span class="font-mono text-ink">{current.sku}</span>
              {current.weight_g > 0 && <> · {fill(labels.weight, 'value', current.weight_g)}</>}
            </p>
            {quote ? (
              <>
                <p class="mt-3 text-sm text-muted">{labels.unitPrice}</p>
                <p class="text-3xl font-bold tracking-tight text-brand-800">
                  {money.format(Number(unitFor(quote, qty)))}
                  <span class="ml-1 text-sm font-normal text-muted">{labels.perUnit}</span>
                </p>
                {quote.tiers.length > 1 && (
                  <table class="mt-4 w-full text-sm">
                    <caption class="mb-1 text-left text-xs font-semibold uppercase tracking-wide text-muted">{labels.volumePricing}</caption>
                    <tbody>
                      {quote.tiers.map((t) => (
                        <tr key={t.min_qty} class={t.unit_price === unitFor(quote, qty) ? 'font-semibold text-brand-800' : 'text-ink'}>
                          <th scope="row" class="py-1 text-left font-normal">{fill(labels.fromQty, 'qty', t.min_qty)}</th>
                          <td class="py-1 text-right">{money.format(Number(t.unit_price))}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
                <p class="mt-3 text-xs text-muted">{labels.taxNote}</p>
              </>
            ) : (
              <p class="mt-3 font-medium text-muted">{labels.priceOnRequest}</p>
            )}
          </>
        ) : (
          <p class="text-sm text-muted">{labels.unavailable}</p>
        )}

        {current && (
          <p class={`mt-4 text-sm font-medium ${stock > 0 ? 'text-emerald-700' : 'text-red-700'}`}>
            {stock > 0 ? fill(labels.inStock, 'count', stock) : labels.outOfStock}
          </p>
        )}

        <form method="post" action={cartAction} class="mt-4 flex items-end gap-3">
          <input type="hidden" name="intent" value="add" />
          <input type="hidden" name="variant_id" value={current?.id ?? ''} />
          <label class="w-24">
            <span class="label">{labels.quantity}</span>
            <input
              type="number"
              name="qty"
              min={1}
              max={Math.max(1, Math.min(stock, 9999))}
              value={qty}
              class="input"
              onInput={(e) => setQty(Math.max(1, Number((e.target as HTMLInputElement).value) || 1))}
            />
          </label>
          <button type="submit" class="btn-primary flex-1 py-3" disabled={!canBuy}>
            {labels.addToCart}
          </button>
        </form>
      </div>
    </div>
  );
}
