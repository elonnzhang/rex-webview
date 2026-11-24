// 照 pane_darwin.m 的写法做同样的事，给基准测试用。
#ifndef CGOBENCH_DARWIN_H
#define CGOBENCH_DARWIN_H

int cgobench_pick_host(void);
int cgobench_msg_send(void);
int cgobench_host_is_active(void);
int cgobench_host_bounds(double *out);
int cgobench_covered(const double *host);
void cgobench_callbacks(int n);

#endif
