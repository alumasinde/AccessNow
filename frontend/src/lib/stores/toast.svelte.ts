export type ToastTone = 'success' | 'error' | 'info';

export type Toast = {
  id: string;
  title: string;
  message?: string;
  tone: ToastTone;
};

class ToastStore {
  items = $state<Toast[]>([]);

  push(title: string, message?: string, tone: ToastTone = 'info') {
    const id = crypto.randomUUID();
    this.items = [...this.items, { id, title, message, tone }];
    window.setTimeout(() => this.dismiss(id), 5200);
    return id;
  }

  success(title: string, message?: string) {
    return this.push(title, message, 'success');
  }

  error(title: string, message?: string) {
    return this.push(title, message, 'error');
  }

  dismiss(id: string) {
    this.items = this.items.filter((item) => item.id !== id);
  }
}

export const toasts = new ToastStore();
