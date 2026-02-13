load 'test_helper/bats-support/load'
load 'test_helper/bats-assert/load'

export xmc="$XMC_BINARY -u artemis -p artemis"

@test "initialize ANYCAST address" {
    run $xmc get queue1
    assert_success
}

@test "send/receive text payload" {
    run $xmc put queue1 HelloWorld
    assert_success

    run $xmc get queue1
    assert_success

    assert_output "HelloWorld"
}

@test "send HelloWorld from STDIN and receive" {
    run bash -c 'echo "HelloWorld" | $xmc put queue1'
    assert_success

    run $xmc get queue1
    assert_success

    assert_output "HelloWorld"
}
