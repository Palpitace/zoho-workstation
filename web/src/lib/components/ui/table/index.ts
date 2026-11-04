// input: shadcn-svelte 组件属性、子内容和样式工具。
// output: 官方 Vega 基础组件的展示能力。
// pos: CLI 生成组件，仅本地文档头纳入约定。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
import Body from "./table-body.svelte";
import Caption from "./table-caption.svelte";
import Cell from "./table-cell.svelte";
import Footer from "./table-footer.svelte";
import Head from "./table-head.svelte";
import Header from "./table-header.svelte";
import Row from "./table-row.svelte";
import Root from "./table.svelte";

export {
	Root,
	Body,
	Caption,
	Cell,
	Footer,
	Head,
	Header,
	Row,
	//
	Root as Table,
	Body as TableBody,
	Caption as TableCaption,
	Cell as TableCell,
	Footer as TableFooter,
	Head as TableHead,
	Header as TableHeader,
	Row as TableRow,
};
