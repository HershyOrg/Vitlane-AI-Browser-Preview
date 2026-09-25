import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../../shared/ui";
import { useLocale } from "../../../shared/i18n";

export function PurchaseCheckConfirmationDialog({ open, onOpenChange, onConfirm }: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onConfirm: () => void;
}) {
  const { l } = useLocale();
  return <Dialog open={open} onOpenChange={onOpenChange}>
    {/* A card asks this from inside a product group's sheet or a product's details, so it sits above both layers. */}
    <DialogContent role="alertdialog" showCloseButton={false} data-purchase-check-confirmation className="purchase-check-confirmation" overlayClassName="purchase-check-confirmation__overlay">
      <DialogHeader>
        <DialogTitle>{l("Record that you purchased this product?", "해당 상품을 구매했다고 기록하시겠습니까?")}</DialogTitle>
        <DialogDescription>{l("This purchase check is used by Vitlane’s product recommendation algorithm.", "해당 구매 체크는 vitlane의 상품 추천 알고리즘에 반영됩니다.")}</DialogDescription>
      </DialogHeader>
      <DialogFooter>
        <Button emphasis="quiet" type="button" onClick={() => onOpenChange(false)}>{l("Cancel", "취소")}</Button>
        <Button emphasis="primary" type="button" onClick={() => { onOpenChange(false); onConfirm(); }}>{l("Confirm", "확인")}</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>;
}
