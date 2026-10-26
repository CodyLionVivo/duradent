// Opens Culqi Checkout v4 (card or Yape) and, when Culqi returns a token,
// submits it with a regular form to our server, which creates the charge.
// The card number never touches Duradent's servers: Culqi tokenizes it.
// Docs: https://docs.culqi.com/es/documentacion/checkout/checkout-custom
import { useState } from 'preact/hooks';

interface Props {
  publicKey: string;
  amountCents: number;
  currency: string;
  email: string;
  title: string;
  /** id of the <form> that posts the token (hidden input name="token"). */
  formId: string;
  allowYape: boolean;
  lang: 'es' | 'en';
  labels: { payNow: string; paying: string; failed: string };
}

declare global {
  interface Window {
    CulqiCheckout?: new (publicKey: string, config: unknown) => CulqiInstance;
  }
}

interface CulqiInstance {
  open(): void;
  close(): void;
  culqi?: () => void;
  token?: { id: string };
  error?: { user_message?: string; merchant_message?: string };
}

const SCRIPT = 'https://js.culqi.com/checkout-js';

function loadScript(): Promise<void> {
  if (window.CulqiCheckout) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const s = document.createElement('script');
    s.src = SCRIPT;
    s.async = true;
    s.onload = () => resolve();
    s.onerror = () => reject(new Error('culqi script'));
    document.head.appendChild(s);
  });
}

export default function CulqiPay({ publicKey, amountCents, currency, email, title, formId, allowYape, lang, labels }: Props) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  async function open() {
    setError('');
    setBusy(true);
    try {
      await loadScript();
      const Checkout = window.CulqiCheckout;
      if (!Checkout) throw new Error('culqi unavailable');
      const culqi = new Checkout(publicKey, {
        settings: { title, currency, amount: amountCents },
        client: { email },
        options: {
          lang,
          installments: false,
          modal: true,
          paymentMethods: { tarjeta: true, yape: allowYape, billetera: false, bancaMovil: false, agente: false, cuotealo: false },
          paymentMethodsSort: ['tarjeta', 'yape'],
        },
        appearance: { theme: 'default', menuType: 'sidebar', defaultStyle: { buttonBackground: '#0c6a6b', priceColor: '#0f5557' } },
      });
      culqi.culqi = () => {
        if (culqi.token) {
          culqi.close();
          const form = document.getElementById(formId) as HTMLFormElement | null;
          const input = form?.querySelector<HTMLInputElement>('input[name="token"]');
          if (form && input) {
            input.value = culqi.token.id;
            form.submit();
            return;
          }
        }
        setBusy(false);
        setError(culqi.error?.user_message || labels.failed);
      };
      culqi.open();
      // The modal has its own controls; let the button be used again if it is closed.
      setTimeout(() => setBusy(false), 1500);
    } catch {
      setBusy(false);
      setError(labels.failed);
    }
  }

  return (
    <div>
      <button type="button" class="btn-primary w-full py-3 text-base" onClick={open} disabled={busy}>
        {busy ? labels.paying : labels.payNow}
      </button>
      {error && (
        <p role="alert" class="mt-2 text-sm text-red-700">
          {error}
        </p>
      )}
    </div>
  );
}
