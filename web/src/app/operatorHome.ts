export type OperatorAccess = {
  marketingAdmin?: boolean;
  phase5Operator?: boolean;
};

// 운영자 진입의 첫 화면은 주문 처리다. 주문 처리 권한이 없는 운영자는
// 공통 운영 현황에서 시작한다.
export function operatorHomePath(access: OperatorAccess): string {
	if (access.phase5Operator) return "/admin/agencyOrder";
	if (access.marketingAdmin) return "/admin/ops";
	return "/";
}
