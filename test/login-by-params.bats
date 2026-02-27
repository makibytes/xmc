load 'test_helper/bats-support/load'
load 'test_helper/bats-assert/load'

export xmc="$XMC_BINARY -u artemis -p artemis"

@test "send/receive text payload" {
    run $xmc send queue1 HelloWorld
    assert_success

    run $xmc receive queue1
    assert_success

    assert_output "HelloWorld"
}

@test "send HelloWorld from STDIN and receive" {
    run bash -c 'echo "HelloWorld" | $xmc send queue1'
    assert_success

    run $xmc receive queue1
    assert_success

    assert_output "HelloWorld"
}
